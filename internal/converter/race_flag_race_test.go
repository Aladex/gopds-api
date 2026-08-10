//go:build race

package converter

// raceDetectorEnabled lets tests tell race-detector builds apart: the
// detector reprices CPU-bound wall time by an order of magnitude, so
// absolute millisecond budgets are only meaningful in non-race runs.
const raceDetectorEnabled = true
