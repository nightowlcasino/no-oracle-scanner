package scanner

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/drand/drand/client"
	drand_http "github.com/drand/drand/client/http"
	drand_logger "github.com/drand/drand/log"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/nats-io/nats.go"
	"github.com/nightowlcasino/nightowl/erg"
	"github.com/nightowlcasino/nightowl/logger"
	"github.com/spf13/viper"
)

const (
	cleanErgUnconfirmedTxInterval = 30 * time.Second
	ergTxInterval                 = 200 * time.Second //400
	oracleAddress                 = "4FC5xSYb7zfRdUhm6oRmE11P2GJqSMY8UARPbHkmXEq6hTinXq4XNWdJs73BEV44MdmJ49Qo"
	rouletteErgoTree              = "101b0400040004000402054a0e20473041c7e13b5f5947640f79f00d3c5df22fad4841191260350bb8c526f9851f040004000514052605380504050404020400040205040404050f05120406050604080509050c040a0e200ef2e4e25f93775412ac620a1da495943c55ea98e72f3e95d1a18d7ace2f676cd809d601b2a5730000d602b2db63087201730100d603b2db6501fe730200d604e4c672010404d605e4c6a70404d6069e7cb2e4c67203041a9a72047303007304d607e4c6a70504d6087e720705d6099972087206d1ed96830301938c7202017305938c7202028cb2db6308a77306000293b2b2e4c67203050c1a720400e4c67201050400c5a79597830601ed937205730795ec9072067308ed9272067309907206730a939e7206730b7208ed949e7206730c7208ec937207730d937207730eed937205730f939e720673107208eded937205731192720973129072097313ed9372057314939e720673157208eded937205731692720973179072097318ed9372057319937208720693c27201e4c6a7060e93cbc27201731a"
	minerFee                      = 1500000 // 0.0015 ERG
)

var (
	combinedHashes []CombinedHashes
	allErgUnconfirmedTxs unconfirmedTxs

	urls = []string{
		"https://api.drand.sh",
		"https://drand.cloudflare.com",
	}
	chainHash, _ = hex.DecodeString("8990e7a9aaed2ffed73dbd7092123d6f289930540d7651336225dc172e51b2ce")

	oracleValue int
	numErgBoxes int

	baseOracleTxStringBytes = []byte(fmt.Sprintf(`{"requests": [{"address": "%s","value": ,"assets": [],"registers": {"R4": "","R5": ""}}],"fee": %d,"inputsRaw": []}`, oracleAddress, minerFee))
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
			logger.WithFields(logger.Fields{
				"caller":      r.URL.Path,
				"retryCount":  retryCount,
			}).Infof(0, "func call to %s failed retrying", r.URL.String())
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

	return service, err
}

func wait(sleepTime time.Duration, c chan bool) {
	time.Sleep(sleepTime)
	c <- true
}

//func calcTxValue() {
//
//}

func (s *Service) cleanErgUnconfirmedTxs(stop chan bool) {
	cleanErgHashMap := make(chan bool, 1)

	for {
		select {
		case <-stop:
			logger.Infof(0, "stopping clean erg unconfirmed txs hash map")
			stop <- true
			return
		case <-cleanErgHashMap:
			for k := range allErgUnconfirmedTxs.untx {
				start := time.Now()
				// Call api explorer to see if tx is present and has atleast 3 confirmations
				ergTx, err := s.ergExplorer.GetErgTx(k)
				if err != nil {
					logger.WithError(err).WithFields(logger.Fields{
						"caller": "GetErgTx",
						"durationMs": time.Since(start).Milliseconds(),
						"txId": k,
					}).Infof(0, "failed call to 'GetErgTx'")
					continue
				}

				if ergTx.Confirmations >= 3 {
					allErgUnconfirmedTxs.delete(k)
					logger.WithFields(logger.Fields{
						"caller": "GetErgTx",
						"durationMs": time.Since(start).Milliseconds(),
						"txId": k,
					}).Infof(0, "removed tx from allErgUnconfirmedTxs hashmap")
				}
			}
			go wait(cleanErgUnconfirmedTxInterval, cleanErgHashMap)
		default:
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
			logger.Infof(0, "stopping drand client")
			stop <- true
			break loop
		case result := <-newRand:
			randomNumber := hex.EncodeToString(result.Randomness())

			logger.WithFields(logger.Fields{
				"round": result.Round(),
				"sig": hex.EncodeToString(result.Signature()),
				"randomness": randomNumber,
			}).Infof(0, "new random number")

			start := time.Now()
			ergUnconfirmedTxs, err := s.ergNode.GetUnconfirmedTxs()
			if err != nil {
				logger.WithError(err).WithFields(logger.Fields{
					"caller": "GetErgUnconfirmedTxs",
					"durationMs": time.Since(start).Milliseconds(),
				}).Infof(0, "failed to get the latest ERG Unconfirmed Txs")
				continue
			}
			logger.WithFields(logger.Fields{
				"caller": "GetErgUnconfirmedTxs",
				"durationMs": time.Since(start).Milliseconds(),
			}).Infof(0, "")

			hash := &CombinedHashes{
				Hash: randomNumber,
			}

			for _, tx := range ergUnconfirmedTxs {
				// TODO: find a better algorithm to check for existing erg txs
				if _, ok := allErgUnconfirmedTxs.get(tx.Id); !ok {
					for _, box := range tx.Outputs {
						if box.ErgoTree == rouletteErgoTree {
							hash.Boxes = append(hash.Boxes, box.BoxId)
							allErgUnconfirmedTxs.set(tx.Id, true)
							numErgBoxes += 1
						}
					}
				}
			}

			hashBytes, _ := json.Marshal(hash)
			s.nats.Publish(viper.Get("nats.random_number_subj").(string), hashBytes)

			logger.WithFields(logger.Fields{
				"sliceLen": len(combinedHashes) + 1,
				"numErgBoxes": len(hash.Boxes),
				"newHash": string(hashBytes)},
			).Infof(0, "appending to combinedHashes")

			combinedHashes = append(combinedHashes, *hash)

			// Need to send ERG oracle tx after configured time or when tx byte size is greater than 2777
			combinedHashesLen := len(combinedHashes)
			r4BytesLen := combinedHashesLen + 1 + (33 * combinedHashesLen)
			r5BytesLen := combinedHashesLen + 2 + (33 * combinedHashesLen)
			totalTxBytesLen := len(baseOracleTxStringBytes) + r4BytesLen + r5BytesLen
			//fmt.Printf("number of bytes baseOracleTxStringBytes - %d\n", len(baseOracleTxStringBytes))
			//fmt.Printf("number of bytes r4 - %d\n", combinedHashesLen + 1 + (33 * combinedHashesLen))
			//fmt.Printf("number of bytes r5 - %d\n", combinedHashesLen + 2 + (33 * combinedHashesLen))
			//fmt.Printf("total tx bytes - %d\n", totalTxBytesLen)
			//fmt.Printf("tx value - %d\n", totalTxBytesLen * 360)
			if time.Now().Local().After(createErgTxInterval) {
				if numErgBoxes > 0 && !viper.Get("nightowl.test_node").(bool) {
					// use minimum tx value if tx byte size is less than 2778
					if totalTxBytesLen < 2778 {
						oracleValue = 1000000
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
          			}`, oracleAddress, oracleValue, r4, r5, minerFee))

					start = time.Now()
					ergTxId, err := s.ergNode.PostErgOracleTx(txToSign)
					if err != nil {
						// TODO: better retry and error handling here
						logger.WithFields(logger.Fields{
							"caller": "PostErgOracleTx",
							"error": err.Error(),
							"durationMs": time.Since(start).Milliseconds(),
						}).Infof(0, "failed to create ERG Tx")
					} else {
						logger.WithFields(logger.Fields{
							"caller": "PostErgOracleTx",
							"ergTxId": fmt.Sprintf("%s", ergTxId),
							"durationMs": time.Since(start).Milliseconds()},
						).Infof(0, "")
					}
				}

				// keep the last 2 indicies
				combinedHashes = combinedHashes[len(combinedHashes)-2:]
				numErgBoxes = 0
				for _, h := range combinedHashes {
					numErgBoxes += len(h.Boxes)
				}

				createErgTxInterval = time.Now().Local().Add(time.Duration(ergTxInterval))
			}
		default:
		}
	}
}

func (s *Service) Start() {
	
	stopScanner := make(chan bool)
	go s.cleanErgUnconfirmedTxs(stopScanner)
	go s.getDrandNumber(stopScanner)

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