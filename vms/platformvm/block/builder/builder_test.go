// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package builder

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow/consensus/snowman"
	"github.com/ava-labs/avalanchego/upgrade/upgradetest"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/crypto/bls/signer/localsigner"
	"github.com/ava-labs/avalanchego/utils/units"
	"github.com/ava-labs/avalanchego/vms/platformvm/block"
	"github.com/ava-labs/avalanchego/vms/platformvm/reward"
	"github.com/ava-labs/avalanchego/vms/platformvm/signer"
	"github.com/ava-labs/avalanchego/vms/platformvm/state"
	"github.com/ava-labs/avalanchego/vms/platformvm/txs"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"

	blockexecutor "github.com/ava-labs/avalanchego/vms/platformvm/block/executor"
	txexecutor "github.com/ava-labs/avalanchego/vms/platformvm/txs/executor"
)

func TestBuildBlockBasic(t *testing.T) {
	require := require.New(t)

	env := newEnvironment(t, upgradetest.Latest)
	env.ctx.Lock.Lock()
	defer env.ctx.Lock.Unlock()

	subnetID := testSubnet1.ID()
	wallet := newWallet(t, env, walletConfig{
		subnetIDs: []ids.ID{subnetID},
	})

	// Create a valid transaction
	tx, err := wallet.IssueCreateChainTx(
		subnetID,
		nil,
		constants.AVMID,
		nil,
		"chain name",
	)
	require.NoError(err)

	// Issue the transaction
	env.ctx.Lock.Unlock()
	require.NoError(env.network.IssueTxFromRPC(tx))
	env.ctx.Lock.Lock()

	txID := tx.ID()
	_, ok := env.mempool.Get(txID)
	require.True(ok)

	// [BuildBlock] should build a block with the transaction
	blkIntf, err := env.Builder.BuildBlock(context.Background())
	require.NoError(err)

	require.IsType(&blockexecutor.Block{}, blkIntf)
	blk := blkIntf.(*blockexecutor.Block)
	require.Len(blk.Txs(), 1)
	require.Equal(txID, blk.Txs()[0].ID())

	// Mempool should not contain the transaction or have marked it as dropped
	_, ok = env.mempool.Get(txID)
	require.False(ok)
	require.NoError(env.mempool.GetDropReason(txID))
}

func TestBuildBlockDoesNotBuildWithEmptyMempool(t *testing.T) {
	require := require.New(t)

	env := newEnvironment(t, upgradetest.Latest)
	env.ctx.Lock.Lock()
	defer env.ctx.Lock.Unlock()

	tx, exists := env.mempool.Peek()
	require.False(exists)
	require.Nil(tx)

	// [BuildBlock] should not build an empty block
	blk, err := env.Builder.BuildBlock(context.Background())
	require.ErrorIs(err, ErrNoPendingBlocks)
	require.Nil(blk)
}

func TestBuildBlockShouldReward(t *testing.T) {
	require := require.New(t)

	env := newEnvironment(t, upgradetest.Latest)
	env.ctx.Lock.Lock()
	defer env.ctx.Lock.Unlock()

	wallet := newWallet(t, env, walletConfig{})

	var (
		now    = env.backend.Clk.Time()
		nodeID = ids.GenerateTestNodeID()

		defaultValidatorStake = 100 * units.MilliAvax
		validatorStartTime    = now.Add(2 * txexecutor.SyncBound)
		validatorEndTime      = validatorStartTime.Add(360 * 24 * time.Hour)
	)

	sk, err := localsigner.New()
	require.NoError(err)
	pop, err := signer.NewProofOfPossession(sk)
	require.NoError(err)

	rewardOwners := &secp256k1fx.OutputOwners{
		Threshold: 1,
		Addrs:     []ids.ShortID{ids.GenerateTestShortID()},
	}

	// Create a valid [AddPermissionlessValidatorTx]
	tx, err := wallet.IssueAddPermissionlessValidatorTx(
		&txs.SubnetValidator{
			Validator: txs.Validator{
				NodeID: nodeID,
				Start:  uint64(validatorStartTime.Unix()),
				End:    uint64(validatorEndTime.Unix()),
				Wght:   defaultValidatorStake,
			},
			Subnet: constants.PrimaryNetworkID,
		},
		pop,
		env.ctx.AVAXAssetID,
		rewardOwners,
		rewardOwners,
		reward.PercentDenominator,
	)
	require.NoError(err)

	// Issue the transaction
	env.ctx.Lock.Unlock()
	require.NoError(env.network.IssueTxFromRPC(tx))
	env.ctx.Lock.Lock()

	txID := tx.ID()
	_, ok := env.mempool.Get(txID)
	require.True(ok)

	// Build and accept a block with the tx
	blk, err := env.Builder.BuildBlock(context.Background())
	require.NoError(err)
	require.IsType(&block.BanffStandardBlock{}, blk.(*blockexecutor.Block).Block)
	require.Equal([]*txs.Tx{tx}, blk.(*blockexecutor.Block).Block.Txs())
	require.NoError(blk.Verify(context.Background()))
	require.NoError(blk.Accept(context.Background()))
	require.True(env.blkManager.SetPreference(blk.ID()))

	// Validator should now be current
	staker, err := env.state.GetCurrentValidator(constants.PrimaryNetworkID, nodeID)
	require.NoError(err)
	require.Equal(txID, staker.TxID)

	// Should be rewarded at the end of staking period
	env.backend.Clk.Set(validatorEndTime)

	for {
		iter, err := env.state.GetCurrentStakerIterator()
		require.NoError(err)
		require.True(iter.Next())
		staker := iter.Value()
		iter.Release()

		// Check that the right block was built
		blk, err := env.Builder.BuildBlock(context.Background())
		require.NoError(err)
		require.NoError(blk.Verify(context.Background()))
		require.IsType(&block.BanffProposalBlock{}, blk.(*blockexecutor.Block).Block)

		expectedTx, err := NewRewardValidatorTx(env.ctx, staker.TxID)
		require.NoError(err)
		require.Equal([]*txs.Tx{expectedTx}, blk.(*blockexecutor.Block).Block.Txs())

		// Commit the [ProposalBlock] with a [CommitBlock]
		proposalBlk, ok := blk.(snowman.OracleBlock)
		require.True(ok)
		options, err := proposalBlk.Options(context.Background())
		require.NoError(err)

		commit := options[0].(*blockexecutor.Block)
		require.IsType(&block.BanffCommitBlock{}, commit.Block)

		require.NoError(blk.Accept(context.Background()))
		require.NoError(commit.Verify(context.Background()))
		require.NoError(commit.Accept(context.Background()))
		require.True(env.blkManager.SetPreference(commit.ID()))

		// Stop rewarding once our staker is rewarded
		if staker.TxID == txID {
			break
		}
	}

	// Staking rewards should have been issued
	rewardUTXOs, err := env.state.GetRewardUTXOs(txID)
	require.NoError(err)
	require.NotEmpty(rewardUTXOs)
}

func TestBuildBlockAdvanceTime(t *testing.T) {
	require := require.New(t)

	env := newEnvironment(t, upgradetest.Latest)
	env.ctx.Lock.Lock()
	defer env.ctx.Lock.Unlock()

	var (
		now      = env.backend.Clk.Time()
		nextTime = now.Add(2 * txexecutor.SyncBound)
	)

	// Add a staker to [env.state]
	require.NoError(env.state.PutCurrentValidator(&state.Staker{
		NextTime: nextTime,
		Priority: txs.PrimaryNetworkValidatorCurrentPriority,
	}))

	// Advance wall clock to [nextTime]
	env.backend.Clk.Set(nextTime)

	// [BuildBlock] should build a block advancing the time to [NextTime]
	blkIntf, err := env.Builder.BuildBlock(context.Background())
	require.NoError(err)

	require.IsType(&blockexecutor.Block{}, blkIntf)
	blk := blkIntf.(*blockexecutor.Block)
	require.Empty(blk.Txs())
	require.IsType(&block.BanffStandardBlock{}, blk.Block)
	standardBlk := blk.Block.(*block.BanffStandardBlock)
	require.Equal(nextTime.Unix(), standardBlk.Timestamp().Unix())
}

func TestBuildBlockForceAdvanceTime(t *testing.T) {
	require := require.New(t)

	env := newEnvironment(t, upgradetest.Latest)
	env.ctx.Lock.Lock()
	defer env.ctx.Lock.Unlock()

	subnetID := testSubnet1.ID()
	wallet := newWallet(t, env, walletConfig{
		subnetIDs: []ids.ID{subnetID},
	})

	// Create a valid transaction
	tx, err := wallet.IssueCreateChainTx(
		subnetID,
		nil,
		constants.AVMID,
		nil,
		"chain name",
	)
	require.NoError(err)

	// Issue the transaction
	env.ctx.Lock.Unlock()
	require.NoError(env.network.IssueTxFromRPC(tx))
	env.ctx.Lock.Lock()

	txID := tx.ID()
	_, ok := env.mempool.Get(txID)
	require.True(ok)

	var (
		now      = env.backend.Clk.Time()
		nextTime = now.Add(2 * txexecutor.SyncBound)
	)

	// Add a staker to [env.state]
	require.NoError(env.state.PutCurrentValidator(&state.Staker{
		NextTime: nextTime,
		Priority: txs.PrimaryNetworkValidatorCurrentPriority,
	}))

	// Advance wall clock to [nextTime] + [txexecutor.SyncBound]
	env.backend.Clk.Set(nextTime.Add(txexecutor.SyncBound))

	// [BuildBlock] should build a block advancing the time to [nextTime],
	// not the current wall clock.
	blkIntf, err := env.Builder.BuildBlock(context.Background())
	require.NoError(err)

	require.IsType(&blockexecutor.Block{}, blkIntf)
	blk := blkIntf.(*blockexecutor.Block)
	require.Equal([]*txs.Tx{tx}, blk.Txs())
	require.IsType(&block.BanffStandardBlock{}, blk.Block)
	standardBlk := blk.Block.(*block.BanffStandardBlock)
	require.Equal(nextTime.Unix(), standardBlk.Timestamp().Unix())
}

func TestBuildBlockInvalidStakingDurations(t *testing.T) {
	require := require.New(t)

	env := newEnvironment(t, upgradetest.Latest)
	env.ctx.Lock.Lock()
	defer env.ctx.Lock.Unlock()

	// Post-Durango, [StartTime] is no longer validated. Staking durations are
	// based on the current chain timestamp and must be validated.
	env.config.UpgradeConfig.DurangoTime = time.Time{}

	wallet := newWallet(t, env, walletConfig{})

	var (
		now                   = env.backend.Clk.Time()
		defaultValidatorStake = 100 * units.MilliAvax

		// Add a validator ending in [MaxStakeDuration]
		validatorEndTime = now.Add(env.config.MaxStakeDuration)
	)

	sk, err := localsigner.New()
	require.NoError(err)
	pop, err := signer.NewProofOfPossession(sk)
	require.NoError(err)

	rewardsOwner := &secp256k1fx.OutputOwners{
		Threshold: 1,
		Addrs:     []ids.ShortID{ids.GenerateTestShortID()},
	}
	tx1, err := wallet.IssueAddPermissionlessValidatorTx(
		&txs.SubnetValidator{
			Validator: txs.Validator{
				NodeID: ids.GenerateTestNodeID(),
				Start:  uint64(now.Unix()),
				End:    uint64(validatorEndTime.Unix()),
				Wght:   defaultValidatorStake,
			},
			Subnet: constants.PrimaryNetworkID,
		},
		pop,
		env.ctx.AVAXAssetID,
		rewardsOwner,
		rewardsOwner,
		reward.PercentDenominator,
	)
	require.NoError(err)
	require.NoError(env.mempool.Add(tx1))

	tx1ID := tx1.ID()
	_, ok := env.mempool.Get(tx1ID)
	require.True(ok)

	// Add a validator ending past [MaxStakeDuration]
	validator2EndTime := now.Add(env.config.MaxStakeDuration + time.Second)

	sk, err = localsigner.New()
	require.NoError(err)
	pop, err = signer.NewProofOfPossession(sk)
	require.NoError(err)

	tx2, err := wallet.IssueAddPermissionlessValidatorTx(
		&txs.SubnetValidator{
			Validator: txs.Validator{
				NodeID: ids.GenerateTestNodeID(),
				Start:  uint64(now.Unix()),
				End:    uint64(validator2EndTime.Unix()),
				Wght:   defaultValidatorStake,
			},
			Subnet: constants.PrimaryNetworkID,
		},
		pop,
		env.ctx.AVAXAssetID,
		rewardsOwner,
		rewardsOwner,
		reward.PercentDenominator,
	)
	require.NoError(err)
	require.NoError(env.mempool.Add(tx2))

	tx2ID := tx2.ID()
	_, ok = env.mempool.Get(tx2ID)
	require.True(ok)

	// Only tx1 should be in a built block since [MaxStakeDuration] is satisfied.
	blkIntf, err := env.Builder.BuildBlock(context.Background())
	require.NoError(err)

	require.IsType(&blockexecutor.Block{}, blkIntf)
	blk := blkIntf.(*blockexecutor.Block)
	require.Len(blk.Txs(), 1)
	require.Equal(tx1ID, blk.Txs()[0].ID())

	// Mempool should have none of the txs
	_, ok = env.mempool.Get(tx1ID)
	require.False(ok)
	_, ok = env.mempool.Get(tx2ID)
	require.False(ok)

	// Only tx2 should be dropped
	require.NoError(env.mempool.GetDropReason(tx1ID))

	tx2DropReason := env.mempool.GetDropReason(tx2ID)
	require.ErrorIs(tx2DropReason, txexecutor.ErrStakeTooLong)
}

func TestBuildBlockFIFOOrder(t *testing.T) {
	require := require.New(t)

	env := newEnvironment(t, upgradetest.Latest)
	env.ctx.Lock.Lock()
	defer env.ctx.Lock.Unlock()

	subnetID := testSubnet1.ID()
	wallet := newWallet(t, env, walletConfig{
		subnetIDs: []ids.ID{subnetID},
	})

	tx1, err := wallet.IssueCreateChainTx(
		testSubnet1.ID(),
		nil,
		constants.AVMID,
		nil,
		"chain name 1",
	)
	require.NoError(err)

	tx2, err := wallet.IssueCreateChainTx(
		testSubnet1.ID(),
		nil,
		constants.AVMID,
		nil,
		"chain name 2",
	)
	require.NoError(err)

	tx3, err := wallet.IssueCreateChainTx(
		testSubnet1.ID(),
		nil,
		constants.AVMID,
		nil,
		"chain name 3",
	)
	require.NoError(err)

	require.Equal(0, env.mempool.Len())

	require.NoError(env.mempool.Add(tx1))
	require.NoError(env.mempool.Add(tx2))
	require.NoError(env.mempool.Add(tx3))

	require.Equal(3, env.mempool.Len())

	blkIntf, err := env.Builder.BuildBlock(context.Background())
	require.NoError(err)

	blk := blkIntf.(*blockexecutor.Block)
	require.Len(blk.Txs(), 3)
	require.Equal(tx1.ID(), blk.Txs()[0].ID())
	require.Equal(tx2.ID(), blk.Txs()[1].ID())
	require.Equal(tx3.ID(), blk.Txs()[2].ID())

	require.Equal(0, env.mempool.Len())
}

func TestStrictFIFOAllProperties(t *testing.T) {
	require := require.New(t)
	env := newEnvironment(t, upgradetest.Latest)
	env.ctx.Lock.Lock()
	defer env.ctx.Lock.Unlock()

	subnetID := testSubnet1.ID()
	wallet := newWallet(t, env, walletConfig{
		subnetIDs: []ids.ID{subnetID},
	})

	var allTxs []*txs.Tx

	/// Light Txs (low size and gas)
	lightTxs := []struct {
		name string
		vmID ids.ID
	}{
		{"light chain 1", constants.AVMID},
		{"light chain 2", constants.AVMID},
		{"light chain 3", constants.AVMID},
	}

	for _, lt := range lightTxs {
		tx, err := wallet.IssueCreateChainTx(
			subnetID,
			nil, // empty genesis = low gas
			lt.vmID,
			nil,
			lt.name,
		)
		require.NoError(err)
		allTxs = append(allTxs, tx)
	}

	/// Heavy Txs (big size and gas)
	heavyGenesisBytes := []byte(strings.Repeat("a", 32*1024))
	heavyTxs := []struct {
		name string
		vmID ids.ID
	}{
		{"heavy chain 1", ids.GenerateTestID()},
		{"heavy chain 2", ids.GenerateTestID()},
		{"heavy chain 3", ids.GenerateTestID()},
	}

	for _, ht := range heavyTxs {
		tx, err := wallet.IssueCreateChainTx(
			subnetID,
			heavyGenesisBytes,
			ht.vmID,
			nil,
			ht.name,
		)
		require.NoError(err)
		allTxs = append(allTxs, tx)
	}

	/// Mixed Txs

	// Small size, high gas
	smallSizeHighGasGenesisBytes := []byte(strings.Repeat("x", 1024))
	smallSizeHighGasTx, err := wallet.IssueCreateChainTx(
		subnetID,
		smallSizeHighGasGenesisBytes,
		ids.GenerateTestID(),
		nil,
		"small size high gas",
	)
	require.NoError(err)
	allTxs = append(allTxs, smallSizeHighGasTx)

	// Big size, low gas
	bigSizeLowGasGenesisBytes := []byte(strings.Repeat("z", 32*1024))
	bigSizeLowGasTx, err := wallet.IssueCreateChainTx(
		subnetID,
		bigSizeLowGasGenesisBytes,
		constants.AVMID,
		nil,
		"big size low gas",
	)
	require.NoError(err)
	allTxs = append(allTxs, bigSizeLowGasTx)

	// Adding into mempool
	for _, tx := range allTxs {
		require.NoError(env.mempool.Add(tx))
	}

	// Get and check blocks
	var processedTxs []*txs.Tx
	for env.mempool.Len() > 0 {
		blkIntf, err := env.Builder.BuildBlock(context.Background())
		require.NoError(err)

		blk := blkIntf.(*blockexecutor.Block)
		blockTxs := blk.Txs()
		processedTxs = append(processedTxs, blockTxs...)

		require.NoError(blk.Verify(context.Background()))
		require.NoError(blk.Accept(context.Background()))
	}

	/// Verifications
	require.Len(processedTxs, len(allTxs))

	for i, tx := range processedTxs {
		require.Equal(allTxs[i].ID(), tx.ID(),
			"Transactions must be processed in strictly the same order in which they were added")
	}

	for _, tx := range allTxs {
		require.NoError(env.mempool.GetDropReason(tx.ID()),
			"No transaction should be discarded")
	}

	require.Zero(env.mempool.Len())
}

func TestStrictFIFOEdgeCases(t *testing.T) {
	require := require.New(t)
	env := newEnvironment(t, upgradetest.Latest)
	env.ctx.Lock.Lock()
	defer env.ctx.Lock.Unlock()

	subnetID := testSubnet1.ID()
	wallet := newWallet(t, env, walletConfig{
		subnetIDs: []ids.ID{subnetID},
	})

	t.Run("TransactionExactlyBlockSize", func(t *testing.T) {
		exactSizeGenesis := []byte(strings.Repeat("a", 64*units.KiB-1000)) // -1000 for metadata
		exactSizeTx, err := wallet.IssueCreateChainTx(
			subnetID,
			exactSizeGenesis,
			ids.GenerateTestID(),
			nil,
			"exact size tx",
		)
		require.NoError(err)
		require.NoError(env.mempool.Add(exactSizeTx))

		blk, err := env.Builder.BuildBlock(context.Background())
		require.NoError(err)

		txs := blk.(*blockexecutor.Block).Txs()
		require.Len(txs, 1)
		require.Equal(exactSizeTx.ID(), txs[0].ID())
	})

	t.Run("EmptyBlockEdgeCases", func(t *testing.T) {
		env := newEnvironment(t, upgradetest.Latest)
		env.ctx.Lock.Lock()
		defer env.ctx.Lock.Unlock()

		// Trying to create block with empty mempool
		blk, err := env.Builder.BuildBlock(context.Background())
		require.ErrorIs(err, ErrNoPendingBlocks)
		require.Nil(blk)

		// Adding a transaction that will definitely not fit
		hugeGenesis := []byte(strings.Repeat("x", 64*units.KiB-1000))
		hugeTx, err := wallet.IssueCreateChainTx(
			subnetID,
			hugeGenesis,
			ids.GenerateTestID(),
			nil,
			"huge tx",
		)
		require.NoError(err)
		require.NoError(env.mempool.Add(hugeTx))

		// Check that the block is not created
		blk, err = env.Builder.BuildBlock(context.Background())
		require.NoError(env.mempool.GetDropReason(hugeTx.ID()))
	})

	t.Run("MultipleTinyTransactions", func(t *testing.T) {
		env := newEnvironment(t, upgradetest.Latest)
		env.ctx.Lock.Lock()
		defer env.ctx.Lock.Unlock()

		var tinyTxs []*txs.Tx
		for i := 0; i < 100; i++ {
			tx, err := wallet.IssueCreateChainTx(
				subnetID,
				nil,
				constants.AVMID,
				nil,
				fmt.Sprintf("tiny tx %d", i),
			)
			require.NoError(err)
			tinyTxs = append(tinyTxs, tx)
			require.NoError(env.mempool.Add(tx))
		}

		// Building all blocks
		var processedTxs []*txs.Tx
		for env.mempool.Len() > 0 {
			blk, err := env.Builder.BuildBlock(context.Background())
			require.NoError(err)

			txs := blk.(*blockexecutor.Block).Txs()
			processedTxs = append(processedTxs, txs...)

			require.NoError(blk.Verify(context.Background()))
			require.NoError(blk.Accept(context.Background()))
		}

		// ПрCheck that all transactions are processed in the correct order - FIFO
		require.Equal(len(tinyTxs), len(processedTxs))
		for i, tx := range processedTxs {
			require.Equal(tinyTxs[i].ID(), tx.ID())
		}
	})
}
