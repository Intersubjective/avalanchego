package manipulation

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/logging"
	"go.uber.org/zap"
)

var (
	globalManipulator *Manipulator
	initOnce          sync.Once
)

func InitGlobalConfig(configJSON string, log logging.Logger) error {
	var err error

	initOnce.Do(func() {
		if configJSON == "" {
			globalManipulator = New(false, false, log)
			log.Info("manipulation disabled", zap.String("reason", "empty config"))
			return
		}

		var config struct {
			Enabled         bool     `json:"enabled"`
			CensoredTxIDs   []string `json:"censored_tx_ids"`
			PriorityTxIDs   []string `json:"priority_tx_ids"`
			DetectInjection bool     `json:"detect_injection"`
		}

		if err = json.Unmarshal([]byte(configJSON), &config); err != nil {
			err = fmt.Errorf("failed to parse config: %w", err)
			log.Error("config parsing failed", zap.Error(err))
			return
		}

		log.Debug("parsed config",
			zap.Bool("enabled", config.Enabled),
			zap.Int("censored_count", len(config.CensoredTxIDs)),
			zap.Int("priority_count", len(config.PriorityTxIDs)),
			zap.Bool("detect_injection", config.DetectInjection))

		globalManipulator = New(config.Enabled, config.DetectInjection, log)

		for _, idStr := range config.CensoredTxIDs {
			txID, parseErr := ids.FromString(idStr)
			if parseErr != nil {
				log.Warn("skipping invalid censored tx ID", zap.String("id", idStr), zap.Error(parseErr))
				continue
			}
			globalManipulator.AddCensoredTxID(txID)
			log.Debug("added censored tx", zap.Stringer("txID", txID))
		}

		for _, idStr := range config.PriorityTxIDs {
			txID, parseErr := ids.FromString(idStr)
			if parseErr != nil {
				log.Warn("skipping invalid priority tx ID", zap.String("id", idStr), zap.Error(parseErr))
				continue
			}
			globalManipulator.AddPriorityTxID(txID)
			log.Debug("added priority tx", zap.Stringer("txID", txID))
		}

		if config.Enabled {
			log.Info("manipulation enabled",
				zap.Int("censored", len(config.CensoredTxIDs)),
				zap.Int("priority", len(config.PriorityTxIDs)),
				zap.Bool("detect_injection", config.DetectInjection))
		} else {
			log.Info("manipulation disabled")
		}
	})

	return err
}

func GetGlobalManipulator() *Manipulator {
	if globalManipulator == nil {
		globalManipulator = &Manipulator{
			Enabled:         false,
			CensoredTxIDs:   make(map[ids.ID]struct{}),
			PriorityTxIDs:   make(map[ids.ID]struct{}),
			DetectInjection: false,
		}
	}
	return globalManipulator
}
