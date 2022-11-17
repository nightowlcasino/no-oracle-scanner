package scanner

import (
	"strings"
	"sync"
	"time"

	"github.com/nightowlcasino/nightowl/erg"
	"go.uber.org/zap"
)

type Retryer struct {
	mu            sync.Mutex
	ergNode 	  *erg.ErgNode
	retryInterval time.Duration
	UnsignedTxs   map[string]UnsignedTx
}

type UnsignedTx struct {
	Payload 	[]byte `json:"payload"`
	RetryCount 	int    `json:"retryCount"`
	LastAttempt string `json:"lastAttempt"`
}

func (r *Retryer) Add(uuid string, payload []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()

	unsTx := UnsignedTx{
		Payload: payload,
		LastAttempt: time.Now().Format(time.RFC3339),
	}

	r.UnsignedTxs[uuid] = unsTx
}

func (r *Retryer) Delete(uuid string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.UnsignedTxs, uuid)
}

func (r *Retryer) RetryLoop() {
	retry := make(chan bool, 1)
	retry <- true

	for {
		<-retry
		// loop through map[string]UnsignedTx and retry each unsigned tx
		for uuid, unsignedTx := range r.UnsignedTxs {
			err := r.RetrySend(uuid)
			if err != nil {
				// update retry count and last attempt time
				r.mu.Lock()
				unsignedTx.RetryCount += 1
				unsignedTx.LastAttempt = time.Now().Format(time.RFC3339)
				r.UnsignedTxs[uuid] = unsignedTx
				r.mu.Unlock()
			}
		}
		go wait(r.retryInterval, retry)
	}
}

func (r *Retryer) RetrySend(uuid string) error {
	start := time.Now()
	ergTxId, err := r.ergNode.PostErgOracleTx(r.UnsignedTxs[uuid].Payload)
	if err != nil {
		log.Error("failed to create erg tx",
			zap.Error(err),
			zap.String("context", "retryer loop"),
			zap.Int("retry_count", r.UnsignedTxs[uuid].RetryCount+1),
			zap.Int64("durationMs", time.Since(start).Milliseconds()),
		)
		return err
	} else {
		log.Info("successfully created erg tx",
			zap.String("erg_tx_id", strings.Trim(string(ergTxId),"\"")),
			zap.String("context", "retryer loop"),
			zap.Int("retry_count", r.UnsignedTxs[uuid].RetryCount),
			zap.Int64("durationMs", time.Since(start).Milliseconds()),
		)
		// remove tx from retry list
		r.Delete(uuid)
	}
	return nil
}