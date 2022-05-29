package main

type EthBlockNumber struct {
  JsonRPC string `json:"jsonrpc"`
  Id      int    `json:"id"`
  Result  string `json:"result"`
}

type Block struct {
  Hash       string `json:"hash"`
  ParentHash string `json:"parentHash"`
  Size       string `json:"size"`
  Timestamp  string `json:"timestamp"`
}

type EthBlock struct {
  JsonRPC string `json:"jsonrpc"`
  Id      int    `json:"id"`
  Block   Block  `json:"result"`
}

type ErgTx struct {
  Id            string        `json:"id"`
	Confirmations int           `json:"numConfirmations,omitempty"`
  Outputs       []ErgTxOutput `json:"outputs"`
}

type ErgTxOutput struct {
  BoxId    string `json:"boxId"`
	ErgoTree string `json:"ergoTree"`
}

type ErgBlock struct {
  HeaderId string  `json:"headerId"`
  Txs      []ErgTx `json:"transactions"`
}