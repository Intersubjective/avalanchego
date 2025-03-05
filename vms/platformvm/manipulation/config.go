// Copyright (C) 2019-2025, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package manipulation

// Config holds the configuration for transaction manipulation
type Config struct {
	// Enabled indicates whether manipulation is enabled for the node
	Enabled bool `json:"enabled"`

	// CensorshipEnabled enables transaction censorship
	CensorshipEnabled bool `json:"censorshipEnabled"`
	// CensoredAddresses is a list of addresses whose transactions should be censored
	CensoredAddresses []string `json:"censoredAddresses"`

	// ReorderingEnabled enables transaction reordering
	ReorderingEnabled bool `json:"reorderingEnabled"`
	// ReorderedAddresses is a list of addresses whose transactions should be prioritized
	ReorderedAddresses []string `json:"reorderedAddresses"`

	// InjectionDetectionEnabled enables detection of transaction injection
	InjectionDetectionEnabled bool `json:"injectionDetectionEnabled"`
}

// NewDefaultConfig returns a default configuration with all manipulations disabled
func NewDefaultConfig() *Config {
	return &Config{
		Enabled:                   false,
		CensorshipEnabled:         false,
		CensoredAddresses:         []string{},
		ReorderingEnabled:         false,
		ReorderedAddresses:        []string{},
		InjectionDetectionEnabled: false,
	}
}
