package manipulation_test

import (
	"testing"

	"github.com/ava-labs/avalanchego/codec"
	"github.com/ava-labs/avalanchego/codec/linearcodec"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/crypto/secp256k1"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/platformvm/txs"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/vms/platformvm/manipulation"
)

func TestManipulator(t *testing.T) {
	logger := logging.NoLog{}
	codecManager := codec.NewManager(1024 * 1024)
	c := linearcodec.New([]string{})

	err := c.RegisterType(&txs.BaseTx{})
	require.NoError(t, err)
	err = c.RegisterType(&secp256k1fx.TransferInput{})
	require.NoError(t, err)
	err = c.RegisterType(&secp256k1fx.TransferOutput{})
	require.NoError(t, err)
	err = c.RegisterType(&secp256k1fx.Credential{})
	require.NoError(t, err)
	err = codecManager.RegisterCodec(txs.CodecVersion, c)
	require.NoError(t, err)

	wallets := make([]*secp256k1.PrivateKey, 5)
	addresses := make([]ids.ShortID, 5)
	for i := 0; i < 5; i++ {
		key, err := secp256k1.NewPrivateKey()
		require.NoError(t, err)
		wallets[i] = key
		addresses[i] = key.PublicKey().Address()
	}
	blockedAddresses := addresses[:2]

	t.Run("Initialization", func(t *testing.T) {
		m := manipulation.New(true, true, logger)
		require.True(t, m.Enabled)
		require.True(t, m.DetectInjection)
		require.NotNil(t, m.CensoredTxIDs)
		require.NotNil(t, m.PriorityTxIDs)
		require.Zero(t, len(m.CensoredTxIDs))
		require.Zero(t, len(m.PriorityTxIDs))
	})

	t.Run("Blocked Addresses Censorship", func(t *testing.T) {
		m := manipulation.New(true, true, logger)
		censor := &struct {
			manipulator      *manipulation.Manipulator
			blockedAddresses map[ids.ShortID]struct{}
			blockedTxIDs     map[ids.ID]struct{}
		}{
			manipulator:      m,
			blockedAddresses: make(map[ids.ShortID]struct{}),
			blockedTxIDs:     make(map[ids.ID]struct{}),
		}

		for _, addr := range blockedAddresses {
			censor.blockedAddresses[addr] = struct{}{}
		}

		isCensored := func(tx *txs.Tx) bool {
			baseTx, ok := tx.Unsigned.(*txs.BaseTx)
			if !ok {
				return false
			}
			for _, out := range baseTx.Outs {
				if transferOut, ok := out.Out.(*secp256k1fx.TransferOutput); ok {
					for _, addr := range transferOut.Addrs {
						if _, exists := censor.blockedAddresses[addr]; exists {
							return true
						}
					}
				}
			}
			return false
		}

		for i, key := range wallets {
			tx := &txs.Tx{
				Unsigned: &txs.BaseTx{
					BaseTx: avax.BaseTx{
						NetworkID:    1,
						BlockchainID: ids.Empty,
						Ins: []*avax.TransferableInput{{
							UTXOID: avax.UTXOID{TxID: ids.GenerateTestID()},
							Asset:  avax.Asset{ID: ids.Empty},
							In:     &secp256k1fx.TransferInput{Amt: 1000, Input: secp256k1fx.Input{SigIndices: []uint32{0}}},
						}},
						Outs: []*avax.TransferableOutput{{
							Asset: avax.Asset{ID: ids.Empty},
							Out: &secp256k1fx.TransferOutput{
								Amt:          1000,
								OutputOwners: secp256k1fx.OutputOwners{Threshold: 1, Addrs: []ids.ShortID{key.PublicKey().Address()}},
							},
						}},
					},
				},
			}
			err := tx.Initialize(codecManager)
			require.NoError(t, err)
			txID := ids.GenerateTestID()
			tx.SetBytes(tx.Unsigned.Bytes(), txID[:])

			censored := isCensored(tx)
			if i < 2 {
				require.True(t, censored)
				m.AddCensoredTxID(tx.ID())
				require.True(t, m.ShouldCensor(tx.ID()))
			} else {
				require.False(t, censored)
				require.False(t, m.ShouldCensor(tx.ID()))
			}
			require.False(t, m.ShouldCensor(ids.GenerateTestID()))
		}
	})

	t.Run("Priority Transactions", func(t *testing.T) {
		m := manipulation.New(true, true, logger)
		key := wallets[2]
		tx := newTx(t, codecManager, key, 1000)
		m.AddPriorityTxID(tx.ID())
		require.True(t, m.ShouldPrioritize(tx.ID()))
		require.False(t, m.ShouldPrioritize(ids.GenerateTestID()))

		tx1 := newTx(t, codecManager, nil, 0)
		tx2 := newTx(t, codecManager, nil, 0)
		tx3 := newTx(t, codecManager, nil, 0)

		m.AddPriorityTxID(tx1.ID())
		m.AddPriorityTxID(tx2.ID())
		txsList := []*txs.Tx{tx1, tx2, tx}
		reordered := m.ApplyReordering(txsList)
		require.Equal(t, 3, len(reordered))
		require.True(t, m.ShouldPrioritize(reordered[0].ID()))
		require.True(t, m.ShouldPrioritize(reordered[1].ID()))
		require.True(t, m.ShouldPrioritize(reordered[2].ID()))

		m = manipulation.New(true, true, logger)
		txsList = []*txs.Tx{tx1, tx2, tx3}
		reordered = m.ApplyReordering(txsList)
		require.Equal(t, txsList, reordered)

		txsList = []*txs.Tx{tx1, tx, tx3}
		m.AddPriorityTxID(tx.ID())
		reordered = m.ApplyReordering(txsList)
		require.Equal(t, tx.ID(), reordered[0].ID())
		require.False(t, m.ShouldPrioritize(reordered[1].ID()))
		require.False(t, m.ShouldPrioritize(reordered[2].ID()))

		shortList := []*txs.Tx{tx1}
		reordered = m.ApplyReordering(shortList)
		require.Equal(t, shortList, reordered)

		emptyList := []*txs.Tx{}
		reordered = m.ApplyReordering(emptyList)
		require.Empty(t, reordered)
	})

	t.Run("Injection Detection", func(t *testing.T) {
		m := manipulation.New(true, true, logger)
		tx := newTx(t, codecManager, nil, 0)
		require.False(t, m.ShouldDropAsInjection(tx, true))
		require.True(t, m.ShouldDropAsInjection(tx, false))
		require.False(t, m.ShouldDropAsInjection(nil, false))

		m = manipulation.New(true, false, logger)
		require.False(t, m.ShouldDropAsInjection(tx, false))

		m = manipulation.New(false, true, logger)
		require.False(t, m.ShouldDropAsInjection(tx, false))
	})

	t.Run("Disabled Manipulations", func(t *testing.T) {
		m := manipulation.New(false, true, logger)
		tx := newTx(t, codecManager, nil, 0)
		m.AddCensoredTxID(tx.ID())
		m.AddPriorityTxID(tx.ID())

		require.False(t, m.ShouldCensor(tx.ID()))
		require.False(t, m.ShouldPrioritize(tx.ID()))
		require.False(t, m.ShouldDropAsInjection(tx, false))

		txsList := []*txs.Tx{tx}
		reordered := m.ApplyReordering(txsList)
		require.Equal(t, txsList, reordered)
	})
}

func newTx(t *testing.T, cm codec.Manager, key *secp256k1.PrivateKey, amt uint64) *txs.Tx {
	tx := &txs.Tx{Unsigned: &txs.BaseTx{BaseTx: avax.BaseTx{NetworkID: 1, BlockchainID: ids.Empty}}}
	if key != nil {
		tx.Unsigned.(*txs.BaseTx).Ins = []*avax.TransferableInput{{
			UTXOID: avax.UTXOID{TxID: ids.GenerateTestID()},
			Asset:  avax.Asset{ID: ids.Empty},
			In:     &secp256k1fx.TransferInput{Amt: amt, Input: secp256k1fx.Input{SigIndices: []uint32{0}}},
		}}
		tx.Unsigned.(*txs.BaseTx).Outs = []*avax.TransferableOutput{{
			Asset: avax.Asset{ID: ids.Empty},
			Out: &secp256k1fx.TransferOutput{
				Amt:          amt,
				OutputOwners: secp256k1fx.OutputOwners{Threshold: 1, Addrs: []ids.ShortID{key.PublicKey().Address()}},
			},
		}}
	}
	err := tx.Initialize(cm)
	require.NoError(t, err)
	txID := ids.GenerateTestID()
	tx.SetBytes(tx.Unsigned.Bytes(), txID[:])
	return tx
}
