package session

import (
	"regexp"
	"strings"
	"testing"
)

// Which part of the scan costs what.
//
// Widening the window from 8 KB to 64 KB took one parse to 34 ms, which is not
// a thing to run every poll of every open conversation. These break the cost
// down rather than guess at it.
func benchNoise(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + i%26)
	}
	return "Opus 5 ctx:42% $1.23 5h:13% wk:34%\n" + string(b)
}

func BenchmarkScanStripANSI(b *testing.B) {
	s := benchNoise(64 << 10)
	b.ReportAllocs()
	for b.Loop() {
		_ = stripANSI(s)
	}
}

func BenchmarkScanEachPattern(b *testing.B) {
	s := stripANSI(benchNoise(64 << 10))
	for _, c := range []struct {
		name string
		re   *regexp.Regexp
	}{
		{"context", slContext},
		{"fiveHour", slFiveHour},
		{"weekly", slWeekly},
		{"cost", slCost},
	} {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = lastMatch(s, c.re)
			}
		})
	}
}

// The model scan, now that it locates candidates with a substring search before
// running the pattern on the bytes after each one.
func BenchmarkScanModel(b *testing.B) {
	s := stripANSI(benchNoise(64 << 10))
	b.ReportAllocs()
	for b.Loop() {
		_ = lastModelMatch(s)
	}
}

// What a plain substring search costs on the same input, for comparison. The
// labels are literals, so finding the last one does not need a regex engine.
func BenchmarkScanLastIndex(b *testing.B) {
	s := stripANSI(benchNoise(64 << 10))
	b.ReportAllocs()
	for b.Loop() {
		_ = strings.LastIndex(s, "5h:")
	}
}

func BenchmarkScanParseMode(b *testing.B) {
	s := benchNoise(64 << 10)
	b.ReportAllocs()
	for b.Loop() {
		_ = ParseMode(s)
	}
}
