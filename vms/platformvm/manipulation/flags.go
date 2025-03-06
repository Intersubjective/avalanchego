// Copyright (C) 2019-2025, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package manipulation

import (
	"strings"
)

const (
	// Prefixes for CLI flags
	ManipulationPrefix = "manipulation"

	// Main manipulation flags
	EnabledFlag = "enabled"

	// Censorship flags
	CensorshipEnabledFlag = "censorship-enabled"
	CensoredAddressesFlag = "censored-addresses"

	// Reordering flags
	ReorderingEnabledFlag  = "reordering-enabled"
	ReorderedAddressesFlag = "reordered-addresses"

	// Injection detection flags
	InjectionDetectionEnabledFlag = "injection-detection-enabled"
)

// GetFlagNames returns the full flag names for P-Chain manipulation
func GetFlagNames() map[string]string {
	manipFlags := map[string]string{
		EnabledFlag:                   "manipulation-enabled",
		CensorshipEnabledFlag:         "manipulation-censorship-enabled",
		CensoredAddressesFlag:         "manipulation-censored-addresses",
		ReorderingEnabledFlag:         "manipulation-reordering-enabled",
		ReorderedAddressesFlag:        "manipulation-reordered-addresses",
		InjectionDetectionEnabledFlag: "manipulation-injection-detection-enabled",
	}

	return manipFlags
}

// GetManipulationKeysFromFlags maps the flag names to the corresponding configuration keys
func GetManipulationKeysFromFlags() map[string]string {
	manipFlags := GetFlagNames()

	// Map CLI flags to config file keys
	flagToKey := make(map[string]string, len(manipFlags))
	for flagName, flagValue := range manipFlags {
		key := strings.Replace(flagValue, "-", ".", -1)
		flagToKey[flagName] = key
	}

	return flagToKey
}

// ConfigPrefix returns the prefix used for manipulation configuration keys
// func ConfigPrefix() string {
// 	return constants.PlatformKeyName + "." + ManipulationPrefix
// }
