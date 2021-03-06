package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/nats-io/nats.go"
	log "github.com/sirupsen/logrus"
)

type ErgClient struct {
	client *retryablehttp.Client
	req    *retryablehttp.Request
}

type CombinedHashes struct {
	Hash  string   `json:"hash"`
	Boxes []string `json:"boxes"`
}

const (
	blockNumberEndpoint           = "/api?module=proxy&action=eth_blockNumber"
	getBlockByNumberEndpoint      = "/api?module=proxy&action=eth_getBlockByNumber"
	getErgUnconfirmedTxsEndpoint  = "/transactions/unconfirmed"
	getErgTxsEndpoint             = "/api/v1/transactions/"
	getLatestBlockHeightEndpoint  = "/blocks/lastHeaders/1"
	getBlocksAtHeightEndpoint     = "/blocks/at/"
	getBlockTxsEndpoint           = "/blocks/"
	postErgOracleTxEndpoint       = "/wallet/transaction/send"
	oracleAddress                 = "4FC5xSYb7zfRdUhm6oRmE11P2GJqSMY8UARPbHkmXEq6hTinXq4XNWdJs73BEV44MdmJ49Qo"
	rouletteErgoTree              = "1012040004000404054a0e203eff84aa4780a9cc612ed429b28c0cb2d17d5d6372a84c4050e68d234743042b0e20afd0d6cb61e86d15f2a0adc1e7e23df532ba3ff35f8ba88bed16729cae9330320400040205040404050f05120406050604080509050c040ad807d601b2a5730000d602b2db63087201730100d603e4c6a70404d6049e7cdb6801b2db6502fe9999a38cc7a7017302007303d6057ee4c6a7050405d6069972057204d607cb7304d1ed96830201938c7202017305938c7202028cb2db6308a7730600029597830501ed9372037307939e720473087205eded9372037309927206730a907206730bed937203730c939e7204730d7205eded937203730e927206730f9072067310ed9372037311937205720493c27201720793c272017207"
	minerFee                      = 1500000 // 0.0015 ERG
	ErgTxInterval                 = 400     // Seconds
	CleanErgUnconfirmedTxInterval = 30      // Seconds
	getEthBlockDelay              = 500     // Milliseconds
)

var apiKey string
var nodeUser string
var nodePassword string
var walletPassword string
var ergNodeApiKey string
var hostname string
var ergNodeFQDN string
var ergExplorerFQDN string
var etherscanFQDN string

var combinedHashes []CombinedHashes

type unconfirmedTxs struct {
	mu   sync.Mutex
	untx map[string]bool
}

var allErgUnconfirmedTxs unconfirmedTxs

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

func getEthBlockNumber(client *retryablehttp.Client, apiKey string) (string, error) {
	var ethBlockNumber EthBlockNumber

	req, err := retryablehttp.NewRequest("GET", etherscanFQDN+blockNumberEndpoint+"&apikey="+apiKey, nil)
	if err != nil {
		return "", fmt.Errorf("error creating getEthBlockNumber request - %s", err.Error())
	}
	req.Header.Set("no-scanner-func", "getEthBlockNumber")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error calling blockNumberEndpoint - %s", err.Error())
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading blockNumResp body - %s", err.Error())
	}

	err = json.Unmarshal(body, &ethBlockNumber)
	if err != nil {
		return "", fmt.Errorf("error unmarshalling EthBlockNumber - %s", err.Error())
	}

	if ethBlockNumber.Result == "Invalid API Key" {
		return "", fmt.Errorf("invalid API key")
	}

	return ethBlockNumber.Result, nil
}

func getEthBlock(client *retryablehttp.Client, apiKey, blockNum string) (EthBlock, error) {
	var ethBlock EthBlock

	req, err := retryablehttp.NewRequest("GET", etherscanFQDN+getBlockByNumberEndpoint+"&tag="+blockNum+"&boolean=true&apikey="+apiKey, nil)
	if err != nil {
		return ethBlock, fmt.Errorf("error creating getEthBlock request - %s", err.Error())
	}
	req.Header.Set("no-scanner-func", "getEthBlock")

	resp, err := client.Do(req)
	if err != nil {
		return ethBlock, fmt.Errorf("error calling getBlockByNumberEndpoint - %s", err.Error())
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return ethBlock, fmt.Errorf("error reading ethBlock body - %s", err.Error())
	}

	err = json.Unmarshal(body, &ethBlock)
	if err != nil {
		return ethBlock, nil
	}

	return ethBlock, nil
}

func (e *ErgClient) getErgUnconfirmedTxs() ([]ErgTx, error) {
	var txs []ErgTx

	resp, err := e.client.Do(e.req)
	if err != nil {
		return txs, fmt.Errorf("error calling getErgUnconfirmedTxsEndpoint - %s", err.Error())
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return txs, fmt.Errorf("error reading erg txs body - %s", err.Error())
	}

	err = json.Unmarshal(body, &txs)
	if err != nil {
		return txs, fmt.Errorf("error unmarshalling ERG unconfirmed Txs - %s", err.Error())
	}

	return txs, nil
}

func (e *ErgClient) unlockWallet() ([]byte, error) {
	var ret []byte

	req, err := retryablehttp.NewRequest("POST", ergNodeFQDN+"/wallet/unlock", bytes.NewBuffer([]byte(fmt.Sprintf("{\"pass\": \"%s\"}", walletPassword))))
	if err != nil {
		return ret, fmt.Errorf("error creating erg node lock wallet request - %s", err.Error())
	}
	req.SetBasicAuth(nodeUser, nodePassword)
	req.Header.Set("api_key", ergNodeApiKey)
	req.Header.Set("no-scanner-func", "lockWallet")
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return ret, fmt.Errorf("error locking erg node wallet - %s", err.Error())
	}

	ret, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		return ret, fmt.Errorf("error parsing erg node lock response - %s", err.Error())
	}

	return ret, nil
}

func (e *ErgClient) lockWallet() ([]byte, error) {
	var ret []byte

	req, err := retryablehttp.NewRequest("GET", ergNodeFQDN+"/wallet/lock", nil)
	if err != nil {
		return ret, fmt.Errorf("error creating erg node lock wallet request - %s", err.Error())
	}
	req.SetBasicAuth(nodeUser, nodePassword)
	req.Header.Set("api_key", ergNodeApiKey)
	req.Header.Set("no-scanner-func", "lockWallet")
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return ret, fmt.Errorf("error locking erg node wallet - %s", err.Error())
	}

	ret, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		return ret, fmt.Errorf("error parsing erg node lock response - %s", err.Error())
	}

	return ret, nil
}

func (e *ErgClient) postErgOracleTx(payload []byte) ([]byte, error) {
	var ret []byte

	_, err := e.unlockWallet()
	if err != nil {
		return ret, err
	}

	defer e.lockWallet()

	req, err := retryablehttp.NewRequest("POST", ergNodeFQDN+postErgOracleTxEndpoint, bytes.NewBuffer(payload))
	if err != nil {
		return ret, fmt.Errorf("error creating postErgOracleTx request - %s", err.Error())
	}
	req.SetBasicAuth(nodeUser, nodePassword)
	req.Header.Set("api_key", ergNodeApiKey)
	req.Header.Set("no-scanner-func", "postErgOracleTx")
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return ret, fmt.Errorf("error submitting erg tx to node - %s", err.Error())
	}

	ret, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		return ret, fmt.Errorf("error parsing erg tx response - %s", err.Error())
	}

	return ret, nil
}

func (e *ErgClient) getErgBoxes(blockHeight string) ([]string, error) {
	var boxes []string

	req, err := retryablehttp.NewRequest("GET", ergNodeFQDN+getBlockTxsEndpoint+blockHeight, nil)
	if err != nil {
		return boxes, fmt.Errorf("error creating getErgBoxes request - %s", err.Error())
	}
	req.SetBasicAuth(nodeUser, nodePassword)
	req.Header.Set("no-scanner-func", "getErgBoxes")

	resp, err := e.client.Do(req)
	if err != nil {
		return boxes, fmt.Errorf("error getting erg boxes - %s", err.Error())
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return boxes, fmt.Errorf("error parsing erg boxes response - %s", err.Error())
	}

	err = json.Unmarshal(body, &boxes)
	if err != nil {
		return boxes, fmt.Errorf("error unmarshalling erg boxes response - %s", err.Error())
	}

	return boxes, nil
}

func (e *ErgClient) getErgTxs(blockId string) (ErgBlock, error) {
	var ergBlock ErgBlock

	req, err := retryablehttp.NewRequest("GET", ergNodeFQDN+getBlocksAtHeightEndpoint+blockId+"/transactions", nil)
	if err != nil {
		return ergBlock, fmt.Errorf("error creating getErgTxs request - %s", err.Error())
	}
	req.SetBasicAuth(nodeUser, nodePassword)
	req.Header.Set("no-scanner-func", "getErgTxs")

	resp, err := e.client.Do(req)
	if err != nil {
		return ergBlock, fmt.Errorf("error getting erg txs - %s", err.Error())
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return ergBlock, fmt.Errorf("error parsing erg txs response - %s", err.Error())
	}

	err = json.Unmarshal(body, &ergBlock)
	if err != nil {
		return ergBlock, fmt.Errorf("error unmarshalling erg txs response - %s", err.Error())
	}

	return ergBlock, nil
}

func reverse(ch *[]CombinedHashes) {
	for i, j := 0, len(*ch)-1; i < j; i, j = i+1, j-1 {
		(*ch)[i], (*ch)[j] = (*ch)[j], (*ch)[i]
	}
}

func main() {

	log.SetFormatter(&log.JSONFormatter{})
	log.SetLevel(log.InfoLevel)

	if value, ok := os.LookupEnv("HOSTNAME"); ok {
		hostname = value
	} else {
		var err error
		hostname, err = os.Hostname()
		if err != nil {
			log.Fatal("unable to get hostname")
		}
	}

	logger := log.WithFields(log.Fields{
		"hostname": hostname,
		"appname":  "no-oracle-scanner",
	})

	if value, ok := os.LookupEnv("ETHERSCAN_API_KEY"); ok {
		apiKey = value
	} else {
		logger.Fatal("ETHERSCAN_API_KEY is a required env variable")
	}

	if value, ok := os.LookupEnv("ERG_NODE_USER"); ok {
		nodeUser = value
	} else {
		logger.Fatal("ERG_NODE_USER is a required env variable")
	}

	if value, ok := os.LookupEnv("ERG_NODE_PASS"); ok {
		nodePassword = value
	} else {
		logger.Fatal("ERG_NODE_PASS is a required env variable")
	}

	if value, ok := os.LookupEnv("ERG_WALLET_PASS"); ok {
		walletPassword = value
	} else {
		logger.Fatal("ERG_WALLET_PASS is a required env variable")
	}

	if value, ok := os.LookupEnv("ERG_NODE_API_KEY"); ok {
		ergNodeApiKey = value
	} else {
		logger.Fatal("ERG_NODE_API_KEY is a required env variable")
	}

	if value, ok := os.LookupEnv("ETHERSCAN_FQDN"); ok {
		etherscanFQDN = "https://" + value
	} else {
		etherscanFQDN = "https://api.etherscan.io"
	}

	if value, ok := os.LookupEnv("ERG_NODE_FQDN"); ok {
		ergNodeFQDN = "https://" + value
	} else {
		ergNodeFQDN = "https://node.nightowlcasino.io"
	}

	if value, ok := os.LookupEnv("ERG_EXPLORER_FQDN"); ok {
		ergExplorerFQDN = "https://" + value
	} else {
		ergExplorerFQDN = "https://ergo-explorer-cdn.getblok.io"
	}

	cleanup := make(chan bool)
	allErgUnconfirmedTxs.untx = make(map[string]bool)
	var start time.Time

	// Connect to NATS server
	nc, err := nats.Connect(nats.DefaultURL)
	if err != nil {
		logger.WithFields(log.Fields{"error": err.Error()}).Error("failed to connect to ':4222' nats server")
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func(logger *log.Entry) {
		<-c
		logger.Info("SIGTERM signal caught, stopping app")
		cleanup <- true
	}(logger)

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
	retryClient.HTTPClient.Timeout = time.Second * 3
	retryClient.Logger = nil
	retryClient.RetryWaitMin = 100 * time.Millisecond
	retryClient.RetryWaitMax = 150 * time.Millisecond
	retryClient.RetryMax = 2
	retryClient.RequestLogHook = func(l retryablehttp.Logger, r *http.Request, i int) {
		retryCount := i
		if retryCount > 0 {
			logger.WithFields(log.Fields{"caller": r.Header.Get("no-scanner-func"), "retryCount": retryCount}).Errorf("func call to %s failed retrying", r.Header.Get("no-scanner-func"))
		}
	}

	req, err := retryablehttp.NewRequest("GET", ergNodeFQDN+getErgUnconfirmedTxsEndpoint, nil)
	if err != nil {
		logger.WithFields(log.Fields{"error": err.Error()}).Fatal("failed to build 'getErgUnconfirmedTxsEndpoint' http request")
	}
	req.Header.Set("no-scanner-func", "getErgUnconfirmedTxs")
	req.SetBasicAuth(nodeUser, nodePassword)

	ergClient := &ErgClient{
		client: retryClient,
		req:    req,
	}

	// go routine to periodically clean erg Unconfirmed Txs hash map
	go func(retryClient *retryablehttp.Client, logger *log.Entry) {
		for {
			select {
			case <-cleanup:
				cleanup <- true
				return
			default:
				for k := range allErgUnconfirmedTxs.untx {
					// Call api explorer to see if tx is present and has atleast 3 confirmations
					req, err := retryablehttp.NewRequest("GET", ergExplorerFQDN+getErgTxsEndpoint+k, nil)
					if err != nil {
						logger.WithFields(log.Fields{"error": err.Error()}).Fatal("failed to build 'getErgTxsEndpoint' http request")
					}
					req.Header.Set("no-scanner-func", "getErgTxs")

					start = time.Now()
					resp, err := retryClient.Do(req)
					if err != nil {
						logger.WithFields(log.Fields{"caller": "getErgTxs", "error": err.Error(), "durationMs": time.Since(start).Milliseconds(), "txId": k}).Error("failed 'getErgTxs' http response")
						continue
					}
					logger.WithFields(log.Fields{"caller": "getErgTxs", "durationMs": time.Since(start).Milliseconds(), "txId": k}).Debug("")

					defer resp.Body.Close()

					body, err := ioutil.ReadAll(resp.Body)
					if err != nil {
						logger.WithFields(log.Fields{"caller": "getErgTxs", "error": err.Error(), "txId": k}).Error("error reading 'getErgTxs' response body")
						continue
					}

					if resp.StatusCode == 200 {
						var tx ErgTx
						err = json.Unmarshal(body, &tx)
						if err != nil {
							logger.WithFields(log.Fields{"caller": "getErgTxs", "error": err.Error(), "txId": k}).Error("error unmarshalling 'getErgTxs' response body")
							continue
						}

						if tx.Confirmations >= 3 {
							allErgUnconfirmedTxs.delete(k)
							logger.WithFields(log.Fields{"caller": "getErgTxs", "durationMs": time.Since(start).Milliseconds(), "txId": k}).Debug("removed tx from allErgUnconfirmedTxs hashmap")
						}
					}
				}
			}
			time.Sleep(CleanErgUnconfirmedTxInterval * time.Second)
		}
	}(retryClient, logger)

	start = time.Now()
	ethBlockNum, err := getEthBlockNumber(retryClient, apiKey)
	if err != nil {
		logger.WithFields(log.Fields{"caller": "getEthBlockNumber", "error": err.Error(), "durationMs": time.Since(start).Milliseconds()}).Fatal("failed to get the latest ETH Block Number")
	}
	logger.WithFields(log.Fields{"caller": "getEthBlockNumber", "durationMs": time.Since(start).Milliseconds(), "ethBlockNum": ethBlockNum}).Info("")

	createErgTxInterval := time.Now().Local().Add(time.Second * time.Duration(ErgTxInterval))

loop:
	for {
		select {
		case <-cleanup:
			break loop
		default:
			start = time.Now()
			ethBlock, err := getEthBlock(retryClient, apiKey, ethBlockNum)
			if err != nil {
				logger.WithFields(log.Fields{"caller": "getEthBlock", "error": err.Error(), "durationMs": time.Since(start).Milliseconds(), "ethBlockNum": ethBlockNum}).Error("failed to get the latest ETH Block")
			}

			// Check that ETH Block exists
			if (Block{}) != ethBlock.Block {
				logger.WithFields(log.Fields{"caller": "getEthBlock", "durationMs": time.Since(start).Milliseconds(), "ethBlockHash": ethBlock.Block.Hash, "ethBlockNum": ethBlockNum}).Info("")

				start = time.Now()
				ergUnconfirmedTxs, err := ergClient.getErgUnconfirmedTxs()
				if err != nil {
					logger.WithFields(log.Fields{"caller": "getErgUnconfirmedTxs", "error": err.Error(), "durationMs": time.Since(start).Milliseconds()}).Error("failed to get the latest ERG Unconfirmed Txs")
				}
				logger.WithFields(log.Fields{"caller": "getErgUnconfirmedTxs", "durationMs": time.Since(start).Milliseconds()}).Info("")

				hash := &CombinedHashes{
					Hash: ethBlock.Block.Hash,
				}

				for _, tx := range ergUnconfirmedTxs {
					// TODO: find a better algorithm to check for existing erg txs
					if _, ok := allErgUnconfirmedTxs.get(tx.Id); !ok {
						for _, box := range tx.Outputs {
							//if box.ErgoTree == rouletteErgoTree {
								hash.Boxes = append(hash.Boxes, box.BoxId)
								allErgUnconfirmedTxs.set(tx.Id, true)
							//}
						}
					}
				}

				hashBytes, _ := json.Marshal(hash)
				nc.Publish("eth.hash", hashBytes)

				logger.WithFields(log.Fields{"sliceLen": len(combinedHashes) + 1, "numErgBoxes": len(hash.Boxes), "newHash": string(hashBytes)}).Info("appending to combinedHashes")

				combinedHashes = append(combinedHashes, *hash)

				// increment hex by 1
				res, err := strconv.ParseInt(ethBlockNum[2:], 16, 0)
				if err != nil {
					logger.WithFields(log.Fields{"caller": "strconv.ParseInt(ethBlockNum[2:], 16, 0)", "error": err.Error()}).Error("")
				}
				ethBlockNum = fmt.Sprintf("0x%x", res+1)

				if time.Now().Local().After(createErgTxInterval) {

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
          			}`, oracleAddress, minerFee, r4, r5, minerFee))

					start = time.Now()
					ergTxId, err := ergClient.postErgOracleTx(txToSign)
					if err != nil {
						logger.WithFields(log.Fields{"caller": "postErgOracleTx", "error": err.Error(), "durationMs": time.Since(start).Milliseconds()}).Error("failed to create ERG Tx")
					}
					logger.WithFields(log.Fields{"caller": "postErgOracleTx", "ergTxId": fmt.Sprintf("%s", ergTxId), "durationMs": time.Since(start).Milliseconds()}).Info("")

					combinedHashes = nil

					createErgTxInterval = time.Now().Local().Add(time.Second * time.Duration(ErgTxInterval))
				}
			}

			time.Sleep(getEthBlockDelay * time.Millisecond)
		}
	}

	close(cleanup)
	os.Exit(0)

}
