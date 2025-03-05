package manipulation

import (
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/crypto/secp256k1"
	"github.com/ava-labs/avalanchego/utils/logging"
	platform "github.com/ava-labs/avalanchego/vms/platformvm/txs"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"go.uber.org/zap"
)

type Manipulator struct {
	Enabled           bool
	CensoredAddresses map[ids.ShortID]struct{}
	PriorityTxIDs     map[ids.ID]struct{}
	DetectInjection   bool
	log               logging.Logger
}

func New(enabled, detectInjection bool, log logging.Logger) *Manipulator {
	return &Manipulator{
		Enabled:           enabled,
		CensoredAddresses: make(map[ids.ShortID]struct{}),
		PriorityTxIDs:     make(map[ids.ID]struct{}),
		DetectInjection:   detectInjection,
		log:               log,
	}
}

func (m *Manipulator) ShouldCensor(tx *platform.Tx) bool {
	if !m.Enabled {
		m.log.Debug("manipulator off, no censor check", zap.Stringer("txID", tx.ID()))
		return false
	}

	unsignedTx := tx.Unsigned
	for _, cred := range tx.Creds {
		if secpCred, ok := cred.(*secp256k1fx.Credential); ok {
			for _, sig := range secpCred.Sigs {
				pubKey, err := secp256k1.RecoverPublicKey(unsignedTx.Bytes(), sig[:])
				if err != nil {
					m.log.Warn("failed to recover public key", zap.Error(err), zap.Stringer("txID", tx.ID()))
					continue
				}
				addr := pubKey.Address()
				if _, censored := m.CensoredAddresses[addr]; censored {
					m.log.Info("censoring tx by sender address",
						zap.Stringer("txID", tx.ID()),
						zap.Stringer("addr", addr))
					return true
				}
			}
		}
	}

	for _, out := range unsignedTx.Outputs() {
		if transferOut, ok := out.Out.(*secp256k1fx.TransferOutput); ok {
			for _, addr := range transferOut.Addrs {
				if _, censored := m.CensoredAddresses[addr]; censored {
					m.log.Info("censoring tx by recipient address",
						zap.Stringer("txID", tx.ID()),
						zap.Stringer("addr", addr))
					return true
				}
			}
		}
	}

	m.log.Debug("tx not censored", zap.Stringer("txID", tx.ID()))
	return false
}

func (m *Manipulator) ShouldPrioritize(txID ids.ID) bool {
	if !m.Enabled {
		m.log.Debug("manipulator off, no priority check", zap.Stringer("txID", txID))
		return false
	}
	_, shouldPrioritize := m.PriorityTxIDs[txID]
	if shouldPrioritize {
		m.log.Info("prioritizing tx", zap.Stringer("txID", txID))
	}
	return shouldPrioritize
}

func (m *Manipulator) ShouldDropAsInjection(tx *platform.Tx, hasTxNumber bool) bool {
	if !m.Enabled || !m.DetectInjection {
		m.log.Debug("injection check skipped", zap.Bool("enabled", m.Enabled), zap.Bool("detectInjection", m.DetectInjection))
		return false
	}
	if !hasTxNumber && tx != nil {
		m.log.Info("possible injection detected", zap.Stringer("txID", tx.ID()), zap.Bool("hasTxNumber", hasTxNumber))
		return true
	}
	m.log.Debug("no injection", zap.Bool("hasTxNumber", hasTxNumber), zap.Any("tx", tx))
	return false
}

func (m *Manipulator) ApplyReordering(txs []*platform.Tx) []*platform.Tx {
	if !m.Enabled || len(txs) <= 1 {
		m.log.Debug("reordering skipped", zap.Bool("enabled", m.Enabled), zap.Int("txCount", len(txs)))
		return txs
	}

	var prioritized, regular []*platform.Tx
	for _, tx := range txs {
		txID := tx.ID()
		if m.ShouldPrioritize(txID) {
			prioritized = append(prioritized, tx)
			m.log.Debug("tx prioritized", zap.Stringer("txID", txID))
		} else {
			regular = append(regular, tx)
			m.log.Debug("tx regular", zap.Stringer("txID", txID))
		}
	}
	m.log.Info("txs reordered", zap.Int("prioritized", len(prioritized)), zap.Int("regular", len(regular)))
	return append(prioritized, regular...)
}

func (m *Manipulator) AddCensoredAddress(addr ids.ShortID) {
	m.CensoredAddresses[addr] = struct{}{}
	m.log.Info("address added to censor list", zap.Stringer("addr", addr))
}

func (m *Manipulator) AddPriorityTxID(txID ids.ID) {
	m.PriorityTxIDs[txID] = struct{}{}
	m.log.Info("tx added to priority list", zap.Stringer("txID", txID))
}
