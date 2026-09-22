package usage

import (
	"runtime"
	"runtime/metrics"
	"sync"
	"time"
)

/*
What this process is costing the machine (USAGE-007).

Measured through `runtime/metrics`, which is portable, exact and needs nothing installed — the
alternative was a process-inspection library, and adding one is not available here. What that
buys and what it costs are both worth stating plainly, because the panel says so too:

  - **Exact for the Go process**: CPU is the share of its allotted time that was not idle, and
    memory is what the runtime holds mapped from the operating system.
  - **It is not the whole application.** The renderer runs in a WebView process of its own, and
    nothing here can see it. So the number is labelled as the CodeFlow process rather than as
    "the app", because a total that quietly excluded half of itself would be the guess this
    feature exists to avoid.

CPU only means anything as a rate, so a reading is the difference since the previous one. The first
reading after launch has nothing to compare against and answers zero rather than a spike.
*/

const (
	metricCPUTotal = "/cpu/classes/total:cpu-seconds"
	metricCPUIdle  = "/cpu/classes/idle:cpu-seconds"
	metricMemory   = "/memory/classes/total:bytes"
)

// Meter turns the runtime's counters into rates. Safe for concurrent use.
type Meter struct {
	mu       sync.Mutex
	lastAt   time.Time
	lastBusy float64
}

func NewMeter() *Meter { return &Meter{} }

// Read takes a sample and answers the rate since the previous one.
func (m *Meter) Read(now time.Time) ResourceUsage {
	samples := []metrics.Sample{
		{Name: metricCPUTotal},
		{Name: metricCPUIdle},
		{Name: metricMemory},
	}
	metrics.Read(samples)

	busy := floatValue(samples[0]) - floatValue(samples[1])

	m.mu.Lock()
	defer m.mu.Unlock()

	previousAt, previousBusy := m.lastAt, m.lastBusy
	m.lastAt, m.lastBusy = now, busy

	reading := ResourceUsage{MemoryBytes: uintValue(samples[2])}
	if previousAt.IsZero() {
		// The first reading is a starting point, not a rate.
		return reading
	}

	// Elapsed wall time against the cores the runtime was given, rather than the runtime's own
	// total counter — that counter advances too coarsely to divide by over a short poll, and the
	// clock is the thing a caller can reason about.
	elapsed := now.Sub(previousAt).Seconds()
	if elapsed <= 0 {
		return reading
	}

	percent := (busy - previousBusy) / (elapsed * float64(runtime.GOMAXPROCS(0))) * 100
	switch {
	case percent < 0:
		percent = 0
	case percent > 100:
		percent = 100
	}

	reading.CPUPercent = percent
	reading.Sampled = true
	return reading
}

func floatValue(sample metrics.Sample) float64 {
	if sample.Value.Kind() != metrics.KindFloat64 {
		return 0
	}
	return sample.Value.Float64()
}

func uintValue(sample metrics.Sample) uint64 {
	if sample.Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return sample.Value.Uint64()
}
