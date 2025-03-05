package manipulation

import (
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/logging"
	platform "github.com/ava-labs/avalanchego/vms/platformvm/txs"
	"go.uber.org/zap"
)

type Manipulator struct {
	Enabled         bool
	CensoredTxIDs   map[ids.ID]struct{}
	PriorityTxIDs   map[ids.ID]struct{}
	DetectInjection bool
	log             logging.Logger
}

func New(enabled, detectInjection bool, log logging.Logger) *Manipulator {
	return &Manipulator{
		Enabled:         enabled,
		CensoredTxIDs:   make(map[ids.ID]struct{}),
		PriorityTxIDs:   make(map[ids.ID]struct{}),
		DetectInjection: detectInjection,
		log:             log,
	}
}

func (m *Manipulator) ShouldCensor(txID ids.ID) bool {
	if !m.Enabled {
		m.log.Debug("manipulator off, no censor check", zap.Stringer("txID", txID))
		return false
	}
	_, shouldCensor := m.CensoredTxIDs[txID]
	if shouldCensor {
		m.log.Info("censoring tx", zap.Stringer("txID", txID))
	} else {
		m.log.Debug("tx not censored", zap.Stringer("txID", txID))
	}
	return shouldCensor
}

func (m *Manipulator) ShouldPrioritize(txID ids.ID) bool {
	if !m.Enabled {
		m.log.Debug("manipulator off, no priority check", zap.Stringer("txID", txID))
		return false
	}
	_, shouldPrioritize := m.PriorityTxIDs[txID]
	if shouldPrioritize {
		m.log.Info("prioritizing tx", zap.Stringer("txID", txID))
	} else {
		m.log.Debug("tx not prioritized", zap.Stringer("txID", txID))
	}
	return shouldPrioritize
}

func (m *Manipulator) ShouldDropAsInjection(tx *platform.Tx, hasTxNumber bool) bool {
	if !m.Enabled || !m.DetectInjection {
		m.log.Debug("injection check skipped", zap.Bool("enabled", m.Enabled), zap.Bool("detectInjection", m.DetectInjection))
		return false
	}
	if !hasTxNumber && tx != nil {
		txID := tx.ID()
		m.log.Info("possible injection detected", zap.Stringer("txID", txID), zap.Bool("hasTxNumber", hasTxNumber))
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

func (m *Manipulator) AddCensoredTxID(txID ids.ID) {
	m.CensoredTxIDs[txID] = struct{}{}
	m.log.Info("tx added to censor list", zap.Stringer("txID", txID))
}

func (m *Manipulator) AddPriorityTxID(txID ids.ID) {
	m.PriorityTxIDs[txID] = struct{}{}
	m.log.Info("tx added to priority list", zap.Stringer("txID", txID))
}
