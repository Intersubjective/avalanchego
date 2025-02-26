package counter

import (
	"sync"
	"testing"

	"github.com/ava-labs/avalanchego/ids"
)

func TestTxCounter(t *testing.T) {
	t.Run("Sequential Incrementation", func(t *testing.T) {
		counter := New()
		txs := []ids.ID{
			ids.GenerateTestID(),
			ids.GenerateTestID(),
			ids.GenerateTestID(),
		}

		for i, txID := range txs {
			expectedNum := uint64(i + 1)
			num := counter.Increment(txID)
			if num != expectedNum {
				t.Errorf("Wrong transaction number: expected %d, got %d", expectedNum, num)
			}
			metadata, exists := counter.GetTxMetadata(txID)
			if !exists {
				t.Errorf("Transaction metadata doesn't exist")
			}
			if metadata.TxID != txID {
				t.Errorf("Incorrect transaction ID in metadata")
			}
			if metadata.Number != expectedNum {
				t.Errorf("Incorrect transaction number in metadata")
			}
		}
		if counter.Get() != 3 {
			t.Errorf("Total counter should be 3, got %d", counter.Get())
		}
	})

	t.Run("Concurrent Incrementation", func(t *testing.T) {
		counter := New()
		const concurrentTxs = 100
		var wg sync.WaitGroup
		var mu sync.Mutex
		seenNumbers := make(map[uint64]bool)

		for i := 0; i < concurrentTxs; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				txID := ids.GenerateTestID()
				num := counter.Increment(txID)
				mu.Lock()
				defer mu.Unlock()
				if _, exists := seenNumbers[num]; exists {
					t.Errorf("Transaction number %d already exists", num)
				}
				seenNumbers[num] = true
			}()
		}
		wg.Wait()
		if len(seenNumbers) != concurrentTxs {
			t.Errorf("Expected %d unique numbers, got %d", concurrentTxs, len(seenNumbers))
		}
	})

	t.Run("Repeated Transaction Handling", func(t *testing.T) {
		counter := New()
		uniqueTxID := ids.GenerateTestID()

		firstNum := counter.Increment(uniqueTxID)
		if firstNum != 1 {
			t.Errorf("First transaction number should be 1, got %d", firstNum)
		}

		secondNum := counter.Increment(uniqueTxID)
		if firstNum != secondNum {
			t.Errorf("Transaction number changed: first %d, second %d", firstNum, secondNum)
		}
	})
}
