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
  getLatestBlockHeightEndpoint  = "/blocks/lastHeaders/1"
  getBlocksAtHeightEndpoint     = "/blocks/at/"
  getBlockTxsEndpoint           = "/blocks/"
  postErgOracleTxEndpoint       = "/wallet/transaction/send"
  oracleAddress                 = "4FC5xSYb7zfRdUhm6oRmE11P2GJqSMY8UARPbHkmXEq6hTinXq4XNWdJs73BEV44MdmJ49Qo"
  minerFee                      = 1500000 // 0.0015 ERG
  ErgTxInterval                 = 400     // Seconds
	CleanErgUnconfirmedTxInterval = 30     // Seconds
  getEthBlockDelay              = 500     // Milliseconds
)

var apiKey string
var nodeUser string
var nodePassword string
var ergNodeApiKey string
var hostname string
var ergNodeFQDN string
var etherscanFQDN string

var combinedHashes []CombinedHashes

type unconfirmedTxs struct {
  mu sync.Mutex
  untx map[string]bool
}
var allErgUnconfirmedTxs unconfirmedTxs

func (u *unconfirmedTxs) delete(key string) {
  u.mu.Lock()
  defer u.mu.Unlock()
  delete(u.untx, key);
}

func getEthBlockNumber(client *retryablehttp.Client, apiKey string) (string, error) {
  var ethBlockNumber EthBlockNumber

  req, err := retryablehttp.NewRequest("GET", etherscanFQDN + blockNumberEndpoint + "&apikey=" + apiKey, nil)
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

  req, err := retryablehttp.NewRequest("GET", etherscanFQDN + getBlockByNumberEndpoint + "&tag=" + blockNum + "&boolean=true&apikey=" + apiKey, nil)
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
    return txs, fmt.Errorf("error unmarshalling EthBlock - %s", err.Error())
  }

  return txs, nil
}

func (e *ErgClient) postErgOracleTx(payload []byte) ([]byte, error) {
  var ret []byte

  req, err := retryablehttp.NewRequest("POST", ergNodeFQDN + postErgOracleTxEndpoint, bytes.NewBuffer(payload))
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

  req, err := retryablehttp.NewRequest("GET", ergNodeFQDN + getBlockTxsEndpoint + blockHeight, nil)
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

  req, err := retryablehttp.NewRequest("GET", ergNodeFQDN + getBlocksAtHeightEndpoint + blockId + "/transactions", nil)
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

  if value, ok := os.LookupEnv("ERG_NODE_API_KEY"); ok {
    ergNodeApiKey = value
  } else {
    logger.Fatal("ERG_NODE_API_KEY is a required env variable")
  }

  if value, ok := os.LookupEnv("ETHERSCAN_FQDN"); ok {
    etherscanFQDN = "https://" + value
  } else {
    //etherscanFQDN = "https://api.etherscan.io"
    etherscanFQDN = "http://localhost:8099"
  }

  if value, ok := os.LookupEnv("ERG_NODE_FQDN"); ok {
    ergNodeFQDN = "https://" + value
  } else {
    //ergNodeFQDN = "https://node.nightowlcasino.io"
    ergNodeFQDN = "http://localhost:8099"
  }

  cleanup := make(chan bool)
  allErgUnconfirmedTxs.untx = make(map[string]bool)
  var start time.Time

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

  req, err := retryablehttp.NewRequest("GET", ergNodeFQDN + getErgUnconfirmedTxsEndpoint, nil)
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
  go func(ergClient *ErgClient, logger *log.Entry) {
    for {
      select {
      case <-cleanup:
				cleanup <- true
        return
      default:
        logger.Info("Get current Erg Height")
      }
      time.Sleep(CleanErgUnconfirmedTxInterval * time.Second)
    }
  }(ergClient, logger)

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
          // TODO: find a better algorithm to check for existing erg txs}
          if _, ok := allErgUnconfirmedTxs.untx[tx.Id]; !ok {
            hash.Boxes = append(hash.Boxes, tx.Id)
            allErgUnconfirmedTxs.untx[tx.Id] = true
          }
        }

        hashBytes, _ := json.Marshal(hash)

        logger.WithFields(log.Fields{"sliceLen": len(combinedHashes) + 1, "numErgBoxes": len(hash.Boxes), "newHash": string(hashBytes)}).Info("appending to combinedHashes")

        combinedHashes = append([]CombinedHashes{*hash}, combinedHashes...)

        // increment hex by 1
        res, err := strconv.ParseInt(ethBlockNum[2:], 16, 0)
        if err != nil {
          logger.WithFields(log.Fields{"caller": "strconv.ParseInt(ethBlockNum[2:], 16, 0)", "error": err.Error()}).Error("")
        }
        ethBlockNum = fmt.Sprintf("0x%x", res+1)

        if time.Now().Local().After(createErgTxInterval) {
          // Remove last 40 elements of []CombinedHashes else remove the last len()/2
          if len(combinedHashes) > 40 {
            combinedHashes = combinedHashes[:len(combinedHashes)-40]
          } else {
            combinedHashes = combinedHashes[:len(combinedHashes)-(len(combinedHashes)/2)]
          }
          reverse(&combinedHashes)

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
          logger.WithFields(log.Fields{"caller": "postErgOracleTx", "ergTxId": fmt.Sprintf("%s",ergTxId), "durationMs": time.Since(start).Milliseconds()}).Info("")

          createErgTxInterval = time.Now().Local().Add(time.Second * time.Duration(ErgTxInterval))
        }
      }

      time.Sleep(getEthBlockDelay * time.Millisecond)
    }
  }

  close(cleanup)
  os.Exit(0)

}
