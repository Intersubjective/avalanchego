package manipulation

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/btcsuite/btcutil/bech32"
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
			Enabled           bool     `json:"enabled"`
			CensoredAddresses []string `json:"censored_addresses"`
			PriorityAddresses []string `json:"priority_tx_ids"`
			DetectInjection   bool     `json:"detect_injection"`
		}
		if err = json.Unmarshal([]byte(configJSON), &config); err != nil {
			err = fmt.Errorf("failed to parse config: %w", err)
			log.Error("config parsing failed", zap.Error(err))
			return
		}

		log.Debug("parsed config",
			zap.Bool("enabled", config.Enabled),
			zap.Int("censored_count", len(config.CensoredAddresses)),
			zap.Int("priority_count", len(config.PriorityAddresses)),
			zap.Bool("detect_injection", config.DetectInjection))

		globalManipulator = New(config.Enabled, config.DetectInjection, log)

		for _, addrStr := range config.CensoredAddresses {
			originalAddr := addrStr
			cleanAddr := strings.TrimPrefix(addrStr, "P-")
			cleanAddr = strings.TrimSpace(cleanAddr)

			log.Debug("processing address",
				zap.String("original", originalAddr),
				zap.String("cleaned", cleanAddr),
				zap.Int("length", len(cleanAddr)))

			hrp, fiveBitData, parseErr := bech32.Decode(cleanAddr)
			if parseErr != nil {
				log.Warn("skipping invalid censored address",
					zap.String("addr", originalAddr),
					zap.String("cleaned", cleanAddr),
					zap.Error(parseErr))
				continue
			}

			if hrp != "avax" {
				log.Warn("skipping address with invalid HRP",
					zap.String("addr", originalAddr),
					zap.String("hrp", hrp),
					zap.String("expected", "avax"))
				continue
			}

			addrBytes, convErr := bech32.ConvertBits(fiveBitData, 5, 8, true)
			if convErr != nil {
				log.Warn("skipping address due to conversion error",
					zap.String("addr", originalAddr),
					zap.Error(convErr))
				continue
			}

			if len(addrBytes) != ids.ShortIDLen {
				log.Warn("skipping address with invalid length",
					zap.String("addr", originalAddr),
					zap.Int("length", len(addrBytes)),
					zap.Int("expected", ids.ShortIDLen))
				continue
			}

			shortID, err := ids.ToShortID(addrBytes)
			if err != nil {
				log.Warn("skipping invalid short ID",
					zap.String("addr", originalAddr),
					zap.Error(err))
				continue
			}

			globalManipulator.AddCensoredAddress(shortID)
			log.Info("added censored address",
				zap.String("original_addr", originalAddr),
				zap.Stringer("addr", shortID))
		}

		for _, addrStr := range config.PriorityAddresses {
			originalAddr := addrStr
			cleanAddr := strings.TrimPrefix(addrStr, "P-")
			cleanAddr = strings.TrimSpace(cleanAddr)

			log.Debug("processing priority address",
				zap.String("original", originalAddr),
				zap.String("cleaned", cleanAddr),
				zap.Int("length", len(cleanAddr)))

			hrp, fiveBitData, parseErr := bech32.Decode(cleanAddr)
			if parseErr != nil {
				log.Warn("skipping invalid priority address",
					zap.String("addr", originalAddr),
					zap.String("cleaned", cleanAddr),
					zap.Error(parseErr))
				continue
			}

			if hrp != "avax" {
				log.Warn("skipping address with invalid HRP",
					zap.String("addr", originalAddr),
					zap.String("hrp", hrp),
					zap.String("expected", "avax"))
				continue
			}

			addrBytes, convErr := bech32.ConvertBits(fiveBitData, 5, 8, true)
			if convErr != nil {
				log.Warn("skipping address due to conversion error",
					zap.String("addr", originalAddr),
					zap.Error(convErr))
				continue
			}

			if len(addrBytes) != ids.ShortIDLen {
				log.Warn("skipping address with invalid length",
					zap.String("addr", originalAddr),
					zap.Int("length", len(addrBytes)),
					zap.Int("expected", ids.ShortIDLen))
				continue
			}

			shortID, err := ids.ToShortID(addrBytes)
			if err != nil {
				log.Warn("skipping invalid short ID",
					zap.String("addr", originalAddr),
					zap.Error(err))
				continue
			}

			globalManipulator.AddPriorityAddress(shortID)
			log.Info("added priority address",
				zap.String("original_addr", originalAddr),
				zap.Stringer("addr", shortID))
		}

		censoredCount := len(globalManipulator.CensoredAddresses)
		priorityCount := len(globalManipulator.PriorityAddresses)

		if config.Enabled {
			log.Info("manipulation enabled",
				zap.Int("censored", censoredCount),
				zap.Int("priority", priorityCount),
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
			Enabled:           false,
			CensoredAddresses: make(map[ids.ShortID]struct{}),
			PriorityAddresses: make(map[ids.ShortID]struct{}),
			DetectInjection:   false,
		}
	}
	return globalManipulator
}
