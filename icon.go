package main

import (
	_ "embed"
)

//go:embed no-phone.ico
var appIconBytes []byte

// getAppIconICO returns the embedded ICO file bytes.
func getAppIconICO() []byte {
	return appIconBytes
}
