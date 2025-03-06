// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package builder

import (
	"context"
	"crypto/rand"
	"fmt"
	mathrand "math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/upgrade/upgradetest"
	"github.com/ava-labs/avalanchego/utils/constants"
	blockexecutor "github.com/ava-labs/avalanchego/vms/platformvm/block/executor"
	"github.com/ava-labs/avalanchego/vms/platformvm/txs"
)

func TestEnhancedFIFOLoadTest(t *testing.T) {
	const (
		numGoroutines   = 10
		txsPerGoroutine = 50
		totalTxs        = numGoroutines * txsPerGoroutine

		maxGenesisSize   = 512
		smallGenesisSize = 128
		testTimeout      = 10 * time.Minute
		maxTxsPerBlock   = 10
		networkLatency   = 20 * time.Millisecond
	)

	type enhancedTxInfo struct {
		tx          *txs.Tx
		index       int
		createdAt   time.Time
		addedAt     time.Time
		processedAt time.Time
	}

	var (
		txsMu         sync.RWMutex
		allTxs        = make([]*enhancedTxInfo, 0, totalTxs)
		txsByID       = make(map[ids.ID]*enhancedTxInfo)
		processedTxs  = make([]*enhancedTxInfo, 0, totalTxs)
		processedTxMu sync.Mutex

		createdTxCount     atomic.Int64
		processedTxCounter atomic.Int64
		rejectedTxCounter  atomic.Int64

		txCreationDone      = make(chan struct{})
		blockProcessingDone = make(chan struct{})
	)

	createEnhancedTx := func(index int, env *environment) (*txs.Tx, error) {
		time.Sleep(time.Duration(mathrand.Intn(50)) * time.Millisecond)

		txType := mathrand.Intn(10)
		var genesis []byte
		var vmID ids.ID

		switch {
		case txType < 2:
			vmID = ids.GenerateTestID()
			genesis = make([]byte, maxGenesisSize)
			if _, err := rand.Read(genesis); err != nil {
				return nil, fmt.Errorf("failed to generate genesis: %v", err)
			}
		default:
			vmID = constants.AVMID
			genesis = make([]byte, smallGenesisSize)
			if _, err := rand.Read(genesis); err != nil {
				return nil, fmt.Errorf("failed to generate genesis: %v", err)
			}
		}

		wallet := newWallet(t, env, walletConfig{
			subnetIDs: []ids.ID{testSubnet1.ID()},
		})

		return wallet.IssueCreateChainTx(
			testSubnet1.ID(),
			genesis,
			vmID,
			nil,
			fmt.Sprintf("tx %d type %d", index, txType),
		)
	}

	require := require.New(t)
	env := newEnvironment(t, upgradetest.Latest)
	env.ctx.Lock.Lock()
	defer env.ctx.Lock.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	var blockProcessingWG sync.WaitGroup
	blockProcessingWG.Add(1)
	go func() {
		defer blockProcessingWG.Done()
		defer close(blockProcessingDone)

		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				select {
				case <-txCreationDone:
				default:
					continue
				}

				if env.mempool.Len() > 0 {
					time.Sleep(networkLatency)

					toProcess := min(env.mempool.Len(), maxTxsPerBlock)

					blkIntf, err := env.Builder.BuildBlock(ctx)
					if err != nil {
						t.Logf("Block build error: %v", err)
						rejectedTxCounter.Add(int64(toProcess))
						continue
					}

					blk := blkIntf.(*blockexecutor.Block)
					if err := blk.Verify(ctx); err != nil {
						t.Logf("Block verification error: %v, block size: %d, num txs: %d",
							err,
							len(blk.Bytes()),
							len(blk.Txs()))
						rejectedTxCounter.Add(int64(toProcess))
						continue
					}

					if err := blk.Accept(ctx); err != nil {
						t.Logf("Block acceptance error: %v", err)
						rejectedTxCounter.Add(int64(toProcess))
						continue
					}

					processedTxMu.Lock()
					now := time.Now()
					for _, tx := range blk.Txs() {
						if info, exists := txsByID[tx.ID()]; exists {
							info.processedAt = now
							processedTxs = append(processedTxs, info)
							processedTxCounter.Add(1)
						}
					}
					processedTxMu.Unlock()
				}

				processedTxMu.Lock()
				txsMu.RLock()
				if len(processedTxs) == len(allTxs) {
					txsMu.RUnlock()
					processedTxMu.Unlock()
					return
				}
				txsMu.RUnlock()
				processedTxMu.Unlock()
			}
		}
	}()

	var txCreationWG sync.WaitGroup
	for g := 0; g < numGoroutines; g++ {
		txCreationWG.Add(1)
		go func(goroutineID int) {
			defer txCreationWG.Done()
			baseIndex := goroutineID * txsPerGoroutine

			for i := 0; i < txsPerGoroutine; i++ {
				select {
				case <-ctx.Done():
					return
				default:
					globalIndex := baseIndex + i
					now := time.Now()

					tx, err := createEnhancedTx(globalIndex, env)
					if err != nil {
						t.Logf("Transaction creation error in goroutine %d: %v", goroutineID, err)
						continue
					}

					txInfo := &enhancedTxInfo{
						tx:        tx,
						index:     globalIndex,
						createdAt: now,
					}

					txsMu.Lock()
					allTxs = append(allTxs, txInfo)
					txsByID[tx.ID()] = txInfo
					txsMu.Unlock()

					if err := env.mempool.Add(tx); err != nil {
						t.Logf("Mempool addition error: %v", err)
						continue
					}
					txInfo.addedAt = time.Now()

					createdTxCount.Add(1)
				}
			}
		}(g)
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				t.Logf("Progress - Created: %d, Processed: %d, Rejected: %d, In mempool: %d",
					createdTxCount.Load(),
					processedTxCounter.Load(),
					rejectedTxCounter.Load(),
					env.mempool.Len(),
				)
			}
		}
	}()

	txCreationWG.Wait()
	close(txCreationDone)

	blockProcessingWG.Wait()

	txsMu.RLock()
	totalExpectedTxs := len(allTxs)
	txsMu.RUnlock()

	processedTxMu.Lock()
	processedTxCount := len(processedTxs)
	processedTxMu.Unlock()

	t.Logf("Total Transactions: %d", totalExpectedTxs)
	t.Logf("Processed Transactions: %d", processedTxCount)
	t.Logf("Rejected Transactions: %d", rejectedTxCounter.Load())

	require.Equal(totalExpectedTxs, processedTxCount,
		"Not all transactions were processed")

	const timeEpsilon = 50 * time.Microsecond

	var lastAddedTime time.Time
	for _, txInfo := range processedTxs {
		timeDiff := txInfo.addedAt.Sub(lastAddedTime)
		require.True(timeDiff >= -timeEpsilon,
			"FIFO order violated: transaction processed out of queue order (time difference: %v)",
			timeDiff)
		lastAddedTime = txInfo.addedAt
	}

	require.Zero(env.mempool.Len(), "Mempool should be empty after processing")
	t.Log("Enhanced FIFO Test Completed Successfully")
}
