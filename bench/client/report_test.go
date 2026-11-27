package main

import (
	"math"
	"testing"
	"time"
)

func entry(service, instance, trace string, at time.Time, latency string) logEntry {
	var e logEntry

	e.Timestamp = at
	e.Trace = "projects/p/traces/" + trace
	e.HTTPRequest.Latency = latency
	e.HTTPRequest.Status = 200
	e.Labels.InstanceID = instance
	e.Resource.Labels.ServiceName = service

	return e
}

// A "cold" request is only counted if it was the first request its instance
// served. This is the check that keeps a warm instance from passing as cold,
// so it has to be able to say no.
func TestJoinConfirmsOnlyFirstRequestOnInstance(t *testing.T) {
	t0 := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)

	logs := []logEntry{
		entry("bench-go", "warm-instance", "earlier", t0, "0.04s"),
		entry("bench-go", "warm-instance", "reused", t0.Add(time.Minute), "0.05s"),
		entry("bench-go", "new-instance", "fresh", t0.Add(time.Minute), "0.6s"),
	}
	results := []result{
		{Service: "go", Kind: "cold", Trace: "reused", Status: 200},
		{Service: "go", Kind: "cold", Trace: "fresh", Status: 200},
		{Service: "go", Kind: "cold", Trace: "unlogged", Status: 200},
	}

	got := join(results, logs)

	if got[0].FirstOnInstance {
		t.Error("a request on an instance that had already served was confirmed as cold")
	}

	if !got[1].FirstOnInstance || got[1].ServerMs != 600 {
		t.Errorf("first request on a new instance: confirmed=%v server=%v ms, want true and 600",
			got[1].FirstOnInstance, got[1].ServerMs)
	}

	if got[2].FirstOnInstance {
		t.Error("a request with no log entry was confirmed as cold")
	}
}

func TestMedianIgnoresNaN(t *testing.T) {
	if got := median([]float64{3, 1, math.NaN(), 2}); got != 2 {
		t.Errorf("median = %v, want 2", got)
	}
}
