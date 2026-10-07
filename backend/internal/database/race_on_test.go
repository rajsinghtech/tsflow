//go:build race

package database

// raceEnabled trims the slowest equality sweeps under -race. The plain test
// run still covers every window.
const raceEnabled = true
