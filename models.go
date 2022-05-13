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
