package analyzer

import (
	"encoding/csv"
	"fmt"
	"io"
	"sort"
	"strconv"
	"time"
)

type Stats struct {
	N      int
	Min    time.Duration
	Median time.Duration
	P50    time.Duration
	P95    time.Duration
	P99    time.Duration
	Max    time.Duration
	Mean   time.Duration
}

func Compute(samplesNs []int64) Stats {
	if len(samplesNs) == 0 {
		return Stats{}
	}
	sorted := append([]int64(nil), samplesNs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	var sum int64
	for _, v := range sorted {
		sum += v
	}
	return Stats{
		N:      len(sorted),
		Min:    time.Duration(sorted[0]),
		Median: time.Duration(percentile(sorted, 50)),
		P50:    time.Duration(percentile(sorted, 50)),
		P95:    time.Duration(percentile(sorted, 95)),
		P99:    time.Duration(percentile(sorted, 99)),
		Max:    time.Duration(sorted[len(sorted)-1]),
		Mean:   time.Duration(sum / int64(len(sorted))),
	}
}

// percentile uses nearest-rank on a pre-sorted slice.
func percentile(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int((p/100.0)*float64(len(sorted)-1) + 0.5)
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

// PrintTable writes a human-readable table to w.
func PrintTable(w io.Writer, s Stats) {
	fmt.Fprintf(w, "  N:      %d\n", s.N)
	fmt.Fprintf(w, "  min:    %s\n", s.Min)
	fmt.Fprintf(w, "  p50:    %s\n", s.P50)
	fmt.Fprintf(w, "  mean:   %s\n", s.Mean)
	fmt.Fprintf(w, "  p95:    %s\n", s.P95)
	fmt.Fprintf(w, "  p99:    %s\n", s.P99)
	fmt.Fprintf(w, "  max:    %s\n", s.Max)
}

// WriteCSV writes one row per sample (trial_index,recovery_time_ns) plus header.
func WriteCSV(w io.Writer, samplesNs []int64) error {
	cw := csv.NewWriter(w)
	defer cw.Flush()
	if err := cw.Write([]string{"trial_index", "recovery_time_ns"}); err != nil {
		return err
	}
	for i, v := range samplesNs {
		if err := cw.Write([]string{strconv.Itoa(i), strconv.FormatInt(v, 10)}); err != nil {
			return err
		}
	}
	return cw.Error()
}
