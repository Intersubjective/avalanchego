package counter

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ava-labs/avalanchego/ids"
)

type TxCounter struct {
	counter   uint64
	lock      sync.RWMutex
	txHistory map[ids.ID]TxMetadata
}

type TxMetadata struct {
	TxID      ids.ID
	Number    uint64
	Timestamp time.Time
}

func New() *TxCounter {
	return &TxCounter{
		counter:   0,
		txHistory: make(map[ids.ID]TxMetadata),
	}
}

func (tc *TxCounter) Increment(txID ids.ID) uint64 {
	tc.lock.RLock()
	if metadata, exists := tc.txHistory[txID]; exists {
		tc.lock.RUnlock()
		return metadata.Number
	}
	tc.lock.RUnlock()

	newValue := atomic.AddUint64(&tc.counter, 1)

	tc.lock.Lock()
	defer tc.lock.Unlock()
	if metadata, exists := tc.txHistory[txID]; exists {
		return metadata.Number
	}

	tc.txHistory[txID] = TxMetadata{
		TxID:      txID,
		Number:    newValue,
		Timestamp: time.Now(),
	}

	return newValue
}

func (tc *TxCounter) Get() uint64 {
	return atomic.LoadUint64(&tc.counter)
}

func (tc *TxCounter) GetTxMetadata(txID ids.ID) (TxMetadata, bool) {
	tc.lock.RLock()
	defer tc.lock.RUnlock()
	metadata, exists := tc.txHistory[txID]
	return metadata, exists
}

func FormatTxID(txID ids.ID, txNum uint64) string {
	return fmt.Sprintf("tx-%d (%s)", txNum, txID.String())
}
