package scanner

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/drand/drand/client"
	drand_http "github.com/drand/drand/client/http"
	drand_logger "github.com/drand/drand/log"
	"github.com/google/uuid"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/nats-io/nats.go"
	"github.com/nightowlcasino/nightowl/erg"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

const (
	cleanErgUnconfirmedTxInterval = 30 * time.Second
	ergTxInterval                 = 200 * time.Second
	oracleAddress                 = "4FC5xSYb7zfRdUhm6oRmE11P2GJqSMY8UARPbHkmXEq6hTinXq4XNWdJs73BEV44MdmJ49Qo"
	rouletteErgoTree              = "101b0400040004000402054a0e20473041c7e13b5f5947640f79f00d3c5df22fad4841191260350bb8c526f9851f040004000514052605380504050404020400040205040404050f05120406050604080509050c040a0e200ef2e4e25f93775412ac620a1da495943c55ea98e72f3e95d1a18d7ace2f676cd809d601b2a5730000d602b2db63087201730100d603b2db6501fe730200d604e4c672010404d605e4c6a70404d6069e7cb2e4c67203041a9a72047303007304d607e4c6a70504d6087e720705d6099972087206d1ed96830301938c7202017305938c7202028cb2db6308a77306000293b2b2e4c67203050c1a720400e4c67201050400c5a79597830601ed937205730795ec9072067308ed9272067309907206730a939e7206730b7208ed949e7206730c7208ec937207730d937207730eed937205730f939e720673107208eded937205731192720973129072097313ed9372057314939e720673157208eded937205731692720973179072097318ed9372057319937208720693c27201e4c6a7060e93cbc27201731a"
	minerFee                      = 1500000 // 0.0015 ERG
)

var (
	combinedHashes []CombinedHashes
	allErgUnconfirmedTxs = unconfirmedTxs{
		untx: make(map[string]bool),
	}
	TxRetryer = Retryer{
		UnsignedTxs: make(map[string]UnsignedTx),
	}

	urls = []string{
		"https://api.drand.sh",
		"https://drand.cloudflare.com",
	}
	chainHash, _ = hex.DecodeString("8990e7a9aaed2ffed73dbd7092123d6f289930540d7651336225dc172e51b2ce")

	numErgBoxes int
	unconfirmedLimit int
	unconfirmedOffset int
	log *zap.Logger

	baseOracleTxStringBytes = []byte(fmt.Sprintf(`{"requests":[{"address":"%s","value":,"assets":[],"registers":{"R4":"","R5":""}}],"fee":%d,"inputsRaw":[]}`, oracleAddress, minerFee))
)

type CombinedHashes struct {
	Hash  string   `json:"hash"`
	Boxes []string `json:"boxes"`
}

type unconfirmedTxs struct {
	mu   sync.Mutex
	untx map[string]bool
}

func (u *unconfirmedTxs) get(key string) (bool, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	val, ok := u.untx[key]
	return val, ok
}

func (u *unconfirmedTxs) set(key string, value bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.untx[key] = value
}

func (u *unconfirmedTxs) delete(key string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	delete(u.untx, key)
}

type Service struct {
	ctx         context.Context
	component   string
	ergNode     *erg.ErgNode
	ergExplorer *erg.Explorer
	drandClient client.Client
	nats        *nats.Conn
	stop        chan bool
	done        chan bool
}

func NewService(nats *nats.Conn) (service *Service, err error) {

	ctx := context.Background()
	log = zap.L()

	t := &http.Transport{
		Dial: (&net.Dialer{
			Timeout: 3 * time.Second,
		}).Dial,
		MaxIdleConns:        100,
		MaxConnsPerHost:     100,
		MaxIdleConnsPerHost: 100,
		TLSHandshakeTimeout: 3 * time.Second,
	}

	retryClient := retryablehttp.NewClient()
	retryClient.HTTPClient.Transport = t
	retryClient.HTTPClient.Timeout = time.Second * 10
	retryClient.Logger = nil
	retryClient.RetryWaitMin = 200 * time.Millisecond
	retryClient.RetryWaitMax = 250 * time.Millisecond
	retryClient.RetryMax = 2
	retryClient.RequestLogHook = func(l retryablehttp.Logger, r *http.Request, i int) {
		retryCount := i
		if retryCount > 0 {
			log.Info("retryClient request failed, retrying...",
				zap.String("url", r.URL.String()),
				zap.Int("retryCount", retryCount),
			)
		}
	}

	ergExplorerClient, err := erg.NewExplorer(retryClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create erg explorer client - %s", err.Error())
	}

	ergNodeClient, err := erg.NewErgNode(retryClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create erg node client - %s", err.Error())
	}

	drandClient, err := client.New(
		client.From(drand_http.ForURLs(urls, chainHash)...),
		client.WithChainHash(chainHash),
		client.WithLogger(drand_logger.NewLogger(drand_logger.LoggerTo(io.Discard), 0)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create drand client - %s", err.Error())
	}

	service = &Service{
		ctx:         ctx,
		component:   "no-oracle-scanner",
		ergNode:     ergNodeClient,
		ergExplorer: ergExplorerClient,
		drandClient: drandClient,
		nats:        nats,
		stop:        make(chan bool),
		done:        make(chan bool),
	}

	TxRetryer.ergNode = ergNodeClient
	TxRetryer.retryInterval = viper.GetDuration("nightowl.tx_send_retry_interval")

	return service, err
}

func wait(sleepTime time.Duration, c chan bool) {
	time.Sleep(sleepTime)
	c <- true
}

func (s *Service) cleanErgUnconfirmedTxs(stop chan bool) {
	cleanErgHashMap := make(chan bool, 1)

	for {
		select {
		case <-stop:
			log.Info("stopping clean erg unconfirmed txs hash map")
			stop <- true
			return
		case <-cleanErgHashMap:
			for k := range allErgUnconfirmedTxs.untx {
				start := time.Now()
				// Call api explorer to see if tx is present and has atleast 3 confirmations
				ergTx, err := s.ergExplorer.GetErgTx(k)
				if err != nil {
					log.Error("failed call to 'GetErgTx'",
						zap.Error(err),
						zap.Int64("durationMs", time.Since(start).Milliseconds()),
						zap.String("tx_id", k),
					)
					continue
				}

				if ergTx.Confirmations >= 3 {
					allErgUnconfirmedTxs.delete(k)
					log.Info("removed tx from allErgUnconfirmedTxs hashmap",
						zap.Int64("durationMs", time.Since(start).Milliseconds()),
						zap.String("tx_id", k),
					)
				}
			}
			go wait(cleanErgUnconfirmedTxInterval, cleanErgHashMap)
		}
	}
}

func (s *Service) getDrandNumber(stop chan bool) {
	newRand := s.drandClient.Watch(s.ctx)
	createErgTxInterval := time.Now().Local().Add(time.Duration(ergTxInterval))

loop:
	for {
		select {
		case <-stop:
			log.Info("stopping drand client")
			stop <- true
			break loop
		case result := <-newRand:
			randomNumber := hex.EncodeToString(result.Randomness())
			var ergUnconfirmedTxOutputs []erg.ErgTxOutputNode = nil
			unconfirmedLimit = 50
			unconfirmedOffset = 0

			log.Info("new random number",
				zap.Uint64("round", result.Round()),
				zap.String("sig", hex.EncodeToString(result.Signature())),
				zap.String("randomness", randomNumber),
			)

			// continuously call GetUnconfirmedOutputsByErgoTree() until we get all tx outputs with the roulette ergo tree
			start := time.Now()
			for {
				start1 := time.Now()
				untxResp, err := s.ergNode.GetUnconfirmedOutputsByErgoTree(rouletteErgoTree, unconfirmedLimit, unconfirmedOffset)
				if err != nil {
					log.Error("failed to get the latest ERG Unconfirmed tx outputs",
						zap.Error(err),
						zap.Int64("durationMs", time.Since(start1).Milliseconds()),
					)
					continue
				}
				log.Debug("number of unconfirmed erg tx outputs",
					zap.Int("outputs_count", len(untxResp)),
					zap.Int64("durationMs", time.Since(start1).Milliseconds()),
				)

				if len(untxResp) == 0 {
					break
				}

				unconfirmedOffset += unconfirmedLimit
				ergUnconfirmedTxOutputs = append(ergUnconfirmedTxOutputs, untxResp...)
			}
			log.Info("finished getting ErgUnconfirmedTxOutputs",
				zap.Int("total_outputs", len(ergUnconfirmedTxOutputs)),
				zap.Int64("durationMs", time.Since(start).Milliseconds()),
			)

			hash := &CombinedHashes{
				Hash: randomNumber,
			}

			for _, output := range ergUnconfirmedTxOutputs {
				if _, ok := allErgUnconfirmedTxs.get(output.TxId); !ok {
					hash.Boxes = append(hash.Boxes, output.BoxId)
					allErgUnconfirmedTxs.set(output.TxId, true)
					numErgBoxes += 1
				}
			}

			hashBytes, _ := json.Marshal(hash)
			s.nats.Publish(viper.Get("nats.random_number_subj").(string), hashBytes)

			log.Info("appending to combinedHashes",
				zap.Int("combined_hashes_len", len(combinedHashes) + 1),
				zap.Int("erg_boxes_len", len(hash.Boxes)),
				zap.String("new_hash", string(hashBytes)),
			)

			combinedHashes = append(combinedHashes, *hash)

			// Need to send all ERG nightowl bets when a configured time is triggered and we found
			// unconfirmed nightowl bets and have collected atleast 2 random numbers
			combinedHashesLen := len(combinedHashes)
			r4BytesLen := combinedHashesLen + 1 + (33 * combinedHashesLen)
			r5BytesLen := combinedHashesLen + 2 + (33 * combinedHashesLen)
			totalTxBytesLen := len(baseOracleTxStringBytes) + r4BytesLen + r5BytesLen
			if time.Now().Local().After(createErgTxInterval) {
				if numErgBoxes > 0 && len(combinedHashes) >= 2 {
					txValue, err := s.ergNode.GetTxFee(totalTxBytesLen)
					if err != nil {
						log.Error("Error getting tx fee from ergo node, defaulting to 2000000",
							zap.Error(err),
						)
						txValue = 2000000
					} else {
						log.Debug("GetTxFee successful",
							zap.Int("tx_bytes_len", totalTxBytesLen),
							zap.Int("oracle_tx_fee", txValue),
						)
					}

					r4 := "1a" + fmt.Sprintf("%02x", len(combinedHashes))
					r5 := "0c1a" + fmt.Sprintf("%02x", len(combinedHashes))

					for _, elem := range combinedHashes {
						r4 = r4 + "20" + elem.Hash[2:]
						boxLen := len(elem.Boxes)

						r5 = r5 + fmt.Sprintf("%02x", boxLen)
						for _, box := range elem.Boxes {
							r5 = r5 + "20" + box
						}
					}

					// Build Erg Tx for node to sign
					txToSign := []byte(fmt.Sprintf(`{
            			"requests": [
              				{
                				"address": "%s",
                				"value": %d,
                				"assets": [],
                				"registers": {
                  					"R4": "%s",
                  					"R5": "%s"
                				}
              				}
            			],
            			"fee": %d,
            			"inputsRaw": []
          			}`, oracleAddress, txValue, r4, r5, minerFee))
					log.Debug("tx for ergo node to sign",
						zap.String("unsigned_tx", string(txToSign)),
					)

					start = time.Now()
					ergTxId, err := s.ergNode.PostErgOracleTx(txToSign)
					if err != nil {
						// add tx to the Retryer which will retry to send this Tx forever or until it is successful
						TxRetryer.Add(uuid.NewString(), txToSign)
						log.Error("failed to create erg tx",
							zap.Error(err),
							zap.Int64("durationMs", time.Since(start).Milliseconds()),
						)
					} else {
						log.Info("successfully created erg tx",
							zap.String("erg_tx_id", strings.Trim(string(ergTxId),"\"")),
							zap.Int64("durationMs", time.Since(start).Milliseconds()),
						)
					}
				}

				// keep the last index for the next batch of random numbers and bets
				if len(combinedHashes) > 1 {
					combinedHashes = combinedHashes[len(combinedHashes)-1:]
					numErgBoxes = 0
					for _, h := range combinedHashes {
						numErgBoxes += len(h.Boxes)
					}
				}

				createErgTxInterval = time.Now().Local().Add(time.Duration(ergTxInterval))
			}
		}
	}
}

func (s *Service) Start() {
	
	stopScanner := make(chan bool)
	go s.cleanErgUnconfirmedTxs(stopScanner)
	go s.getDrandNumber(stopScanner)
	go TxRetryer.RetryLoop()

	// Wait for a "stop" message in the background to stop the service.
	go func(stopScanner chan bool) {
		go func() {
			<-s.stop
			stopScanner <- true
			defer s.drandClient.Close()
			s.done <- true
		}()
	}(stopScanner)
}

func (s *Service) Stop() {
	s.stop <- true
}

func (s *Service) Wait() {
	<-s.done
}