// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package builder

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/upgrade/upgradetest"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/units"
	blockexecutor "github.com/ava-labs/avalanchego/vms/platformvm/block/executor"
	"github.com/ava-labs/avalanchego/vms/platformvm/txs"
)

func TestLoadFIFOUnderHighConcurrency(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping load test in short mode")
	}

	require := require.New(t)
	env := newEnvironment(t, upgradetest.Latest)
	env.ctx.Lock.Lock()
	defer env.ctx.Lock.Unlock()

	subnetID := testSubnet1.ID()
	wallet := newWallet(t, env, walletConfig{
		subnetIDs: []ids.ID{subnetID},
	})

	const (
		numGoroutines    = 5
		txsPerGoroutine  = 45 // Fewer transactions
		totalTxs         = numGoroutines * txsPerGoroutine
		maxGenesisSize   = 2 * units.KiB
		smallGenesisSize = 128
		testTimeout      = 2 * time.Minute
		maxTxsPerBlock   = 10
	)

	type txInfo struct {
		tx    *txs.Tx
		index int
	}

	createRandomTx := func(index int) (*txs.Tx, error) {
		txType := rand.Intn(10)
		var genesis []byte
		var vmID ids.ID

		switch txType {
		case 0, 1: // 20% heavy transactions
			vmID = ids.GenerateTestID()
			genesis = []byte(strings.Repeat("h", maxGenesisSize))
		case 2, 3: // 20% medium transactions
			vmID = ids.GenerateTestID()
			genesis = []byte(strings.Repeat("m", maxGenesisSize/2))
		default: // 60% light transactions
			vmID = constants.AVMID
			genesis = []byte(strings.Repeat("l", smallGenesisSize))
		}

		return wallet.IssueCreateChainTx(
			subnetID,
			genesis,
			vmID,
			nil,
			fmt.Sprintf("tx %d type %d", index, txType),
		)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	var (
		txsMu         sync.Mutex
		allTxs        = make([]*txs.Tx, 0, totalTxs)
		txsByID       = make(map[ids.ID]txInfo)
		wg            sync.WaitGroup
		processedTxs  []*txs.Tx
		processedTxMu sync.Mutex
	)

	progressTicker := time.NewTicker(5 * time.Second)
	defer progressTicker.Stop()

	processingDone := make(chan struct{})

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-progressTicker.C:
				processedTxMu.Lock()
				numProcessed := len(processedTxs)
				processedTxMu.Unlock()

				txsMu.Lock()
				numTotal := len(allTxs)
				txsMu.Unlock()

				t.Logf("Progress: mempool size=%d, processed=%d, total=%d",
					env.mempool.Len(), numProcessed, numTotal)
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(processingDone)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if env.mempool.Len() > 0 {
					toProcess := min(env.mempool.Len(), maxTxsPerBlock)
					t.Logf("Building block with %d transactions (mempool size: %d)",
						toProcess, env.mempool.Len())

					blkIntf, err := env.Builder.BuildBlock(ctx)
					if err != nil {
						t.Logf("Failed to build block: %v", err)
						time.Sleep(time.Second)
						continue
					}

					blk := blkIntf.(*blockexecutor.Block)
					if err := blk.Verify(ctx); err != nil {
						t.Logf("Failed to verify block: %v", err)
						continue
					}

					if err := blk.Accept(ctx); err != nil {
						t.Logf("Failed to accept block: %v", err)
						continue
					}

					processedTxMu.Lock()
					processedTxs = append(processedTxs, blk.Txs()...)
					currentProcessed := len(processedTxs)
					processedTxMu.Unlock()

					t.Logf("Successfully processed block with %d transactions, total processed: %d",
						len(blk.Txs()), currentProcessed)
				} else {
					processedTxMu.Lock()
					txsMu.Lock()
					if len(processedTxs) == len(allTxs) {
						txsMu.Unlock()
						processedTxMu.Unlock()
						return
					}
					txsMu.Unlock()
					processedTxMu.Unlock()
				}
			}
		}
	}()

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			baseIndex := goroutineID * txsPerGoroutine

			for i := 0; i < txsPerGoroutine; i++ {
				select {
				case <-ctx.Done():
					t.Logf("Transaction creation goroutine %d stopping", goroutineID)
					return
				default:
				}

				globalIndex := baseIndex + i
				tx, err := createRandomTx(globalIndex)
				if err != nil {
					t.Logf("Goroutine %d failed to create transaction %d: %v",
						goroutineID, globalIndex, err)
					continue
				}

				txsMu.Lock()
				allTxs = append(allTxs, tx)
				txsByID[tx.ID()] = txInfo{
					tx:    tx,
					index: len(allTxs) - 1,
				}
				txsMu.Unlock()

				if err := env.mempool.Add(tx); err != nil {
					t.Logf("Goroutine %d failed to add transaction %d to mempool: %v",
						goroutineID, globalIndex, err)
					continue
				}

				if i%10 == 0 {
					t.Logf("Goroutine %d created %d/%d transactions",
						goroutineID, i+1, txsPerGoroutine)
				}

				time.Sleep(time.Millisecond)
			}
			t.Logf("Goroutine %d completed creating transactions", goroutineID)
		}(g)
	}

	creationDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(creationDone)
	}()

	select {
	case <-ctx.Done():
		t.Fatal("Test timed out")
	case <-creationDone:
		t.Log("All transactions created")
		select {
		case <-ctx.Done():
			t.Fatal("Test timed out waiting for processing")
		case <-processingDone:
			t.Log("All transactions processed")
		}
	}

	for env.mempool.Len() > 0 {
		t.Log("Waiting for remaining transactions to be processed")
		time.Sleep(time.Second)
	}

	txsMu.Lock()
	numTotalTxs := len(allTxs)
	txsMu.Unlock()

	processedTxMu.Lock()
	numProcessedTxs := len(processedTxs)
	processedTxMu.Unlock()

	require.Equal(numTotalTxs, numProcessedTxs,
		"Number of processed transactions (%d) doesn't match total transactions (%d)",
		numProcessedTxs, numTotalTxs)

	var lastIndex int = -1
	for _, tx := range processedTxs {
		info, exists := txsByID[tx.ID()]
		require.True(exists, "Transaction %s was processed but not found in created transactions", tx.ID())
		require.Greater(info.index, lastIndex,
			"FIFO order violated: transaction %d processed after %d", info.index, lastIndex)
		lastIndex = info.index
	}

	require.Zero(env.mempool.Len(), "Mempool is not empty after processing all transactions")
	t.Log("Test completed successfully")
}
