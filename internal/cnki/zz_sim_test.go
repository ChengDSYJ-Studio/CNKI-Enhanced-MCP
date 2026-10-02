package cnki

import (
	"fmt"
	"math/rand/v2"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"
)

func simEnv(name string, def int) time.Duration {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil {
		return time.Duration(v) * time.Millisecond
	}
	return time.Duration(def) * time.Millisecond
}

func TestSimBenchmarkRewrite(t *testing.T) {
	if os.Getenv("CNKI_SIM_BENCH") == "" {
		t.Skip("set CNKI_SIM_BENCH=1")
	}
	for _, pageSize := range []int{20, 50} {
		store, _ := OpenStore(t.TempDir())
		rng := rand.New(rand.NewPCG(1, 2))
		var mu sync.Mutex
		site := newSim(simEnv("SIM_INTERVAL_MS", 500), func(kind string) time.Duration {
			base := map[string]time.Duration{"grid": simEnv("SIM_GRID_MS", 700), "detail": simEnv("SIM_DETAIL_MS", 450), "journal": simEnv("SIM_JOURNAL_MS", 600)}[kind]
			mu.Lock()
			defer mu.Unlock()
			return time.Duration(float64(base) * (0.7 + 0.6*rng.Float64()))
		})
		site.pageSize = pageSize
		in, plan := simInput(t)
		rec, sum, elapsed := runSim(t, site, store, budget(in.Options), &searchState{Input: in, Plan: plan})
		interval := simEnv("SIM_INTERVAL_MS", 500)
		fmt.Printf("SIM page_size=%d status=%s requests=%d elapsed=%.2fs rate_bound=%.2fs candidates=%d enhanced=%d peak_inflight=%d\n", pageSize, rec.Status, rec.Requests, elapsed.Seconds(), (time.Duration(rec.Requests) * interval).Seconds(), sum.Candidates, sum.Enhanced, site.peak.Load())
		store.Close()
	}
}
