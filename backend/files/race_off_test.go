//go:build !race

package files_test

// raceEnabled is false without `-race`; see race_on_test.go.
const raceEnabled = false
