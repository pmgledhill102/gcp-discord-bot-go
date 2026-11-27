package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
	"time"
)

// logEntry is the part of a Cloud Run request log entry the report reads, as
// exported by `gcloud logging read --format=json`.
type logEntry struct {
	Timestamp   time.Time `json:"timestamp"`
	Trace       string    `json:"trace"`
	HTTPRequest struct {
		Latency string `json:"latency"`
		Status  int    `json:"status"`
	} `json:"httpRequest"`
	Labels struct {
		InstanceID string `json:"instanceId"`
	} `json:"labels"`
	Resource struct {
		Labels struct {
			ServiceName string `json:"service_name"`
		} `json:"labels"`
	} `json:"resource"`
}

type joined struct {
	result

	ServerMs float64
	Instance string
	// FirstOnInstance is true when no earlier logged request reached the same
	// instance: the evidence that a cold request really was cold.
	FirstOnInstance bool
}

func report(args []string) error {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	resultsFile := fs.String("results", "", "JSON Lines written by run")
	logsFile := fs.String("logs", "", "JSON array from gcloud logging read")
	_ = fs.Parse(args)

	if *resultsFile == "" || *logsFile == "" {
		return errors.New("-results and -logs are required")
	}

	results, err := readResults(*resultsFile)
	if err != nil {
		return err
	}

	b, err := os.ReadFile(*logsFile)
	if err != nil {
		return err
	}

	var logs []logEntry
	if err := json.Unmarshal(b, &logs); err != nil {
		return fmt.Errorf("%s: %w", *logsFile, err)
	}

	rows := join(results, logs)
	printReport(rows)

	return nil
}

func readResults(file string) ([]result, error) {
	f, err := os.Open(file) // #nosec G304 -- the operator names their own results file
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []result

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r result
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, err
		}

		out = append(out, r)
	}

	return out, sc.Err()
}

func join(results []result, logs []logEntry) []joined {
	byTrace := map[string]logEntry{}
	// Earliest request seen on each instance, per service.
	firstOn := map[string]time.Time{}

	for _, e := range logs {
		if i := strings.LastIndex(e.Trace, "/"); i >= 0 {
			byTrace[e.Trace[i+1:]] = e
		}

		k := e.Resource.Labels.ServiceName + "/" + e.Labels.InstanceID
		if t, ok := firstOn[k]; !ok || e.Timestamp.Before(t) {
			firstOn[k] = e.Timestamp
		}
	}

	out := make([]joined, 0, len(results))

	for _, r := range results {
		j := joined{result: r, ServerMs: math.NaN()}

		if e, ok := byTrace[r.Trace]; ok {
			if d, err := time.ParseDuration(e.HTTPRequest.Latency); err == nil {
				j.ServerMs = ms(d)
			}

			j.Instance = e.Labels.InstanceID
			j.FirstOnInstance = firstOn[e.Resource.Labels.ServiceName+"/"+j.Instance].Equal(e.Timestamp)
		}

		out = append(out, j)
	}

	return out
}

func median(xs []float64) float64 {
	xs = slices.DeleteFunc(slices.Clone(xs), math.IsNaN)
	if len(xs) == 0 {
		return math.NaN()
	}

	slices.Sort(xs)

	if n := len(xs); n%2 == 1 {
		return xs[n/2]
	} else {
		return (xs[n/2-1] + xs[n/2]) / 2
	}
}

func printReport(rows []joined) {
	type agg struct {
		coldServer, coldClient, warmServer, warmClient []float64
		cold, confirmed, failed                        int
	}

	stats := map[string]*agg{}

	var names []string

	for _, r := range rows {
		a, ok := stats[r.Service]
		if !ok {
			a = &agg{}
			stats[r.Service] = a
			names = append(names, r.Service)
		}

		if r.Status != 200 || r.Err != "" {
			a.failed++

			continue
		}

		if r.Kind == "cold" {
			a.cold++

			if !r.FirstOnInstance {
				// A "cold" request that landed on an instance already serving
				// is not a cold start; leave it out rather than flatter anyone.
				continue
			}

			a.confirmed++
			a.coldServer = append(a.coldServer, r.ServerMs)
			a.coldClient = append(a.coldClient, r.TotalMs)
		} else {
			a.warmServer = append(a.warmServer, r.ServerMs)
			a.warmClient = append(a.warmClient, r.TotalMs)
		}
	}

	slices.SortFunc(names, func(x, y string) int {
		return int(median(stats[x].coldServer) - median(stats[y].coldServer))
	})

	fmt.Println("| Service | Cold, server (median) | Cold, server (range) | Warm, server (median) | Cold, client (median) | Cold rounds confirmed | Failed requests |")
	fmt.Println("| ------- | --------------------: | -------------------: | --------------------: | --------------------: | --------------------: | --------------: |")

	for _, n := range names {
		a := stats[n]
		lo, hi := math.NaN(), math.NaN()

		if len(a.coldServer) > 0 {
			lo, hi = slices.Min(a.coldServer), slices.Max(a.coldServer)
		}

		fmt.Printf("| %s | %s | %s–%s | %s | %s | %d of %d | %d |\n",
			n, msf(median(a.coldServer)), msf(lo), msf(hi), msf(median(a.warmServer)),
			msf(median(a.coldClient)), a.confirmed, a.cold, a.failed)
	}
}

func msf(v float64) string {
	if math.IsNaN(v) {
		return "–"
	}

	return fmt.Sprintf("%s ms", commas(int(math.Round(v))))
}

func commas(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}

	return s
}
