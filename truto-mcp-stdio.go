package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
)

type job struct {
	message string
}
 
type result struct {
	response string
	err      error
}

func worker(id int, jobs <-chan job, results chan<- result, apiURL string) {
	for j := range jobs {
		// Make POST request to the API
		resp, err := http.Post(apiURL, "application/json", bytes.NewBufferString(j.message))
		if err != nil {
			results <- result{err: fmt.Errorf("worker %d: error making request: %v", id, err)}
			continue
		}

		// Read the response body
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			results <- result{err: fmt.Errorf("worker %d: error reading response: %v", id, err)}
			continue
		}

		results <- result{response: string(body)}
	}
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "Usage: mcp-stdio-proxy <API_URL>")
		os.Exit(1)
	}

	apiURL := os.Args[1]
	numWorkers := 10 // Number of concurrent workers

	// Create channels for jobs and results
	jobs := make(chan job, 100)
	results := make(chan result, 100)

	// Start workers
	var wg sync.WaitGroup
	for w := 1; w <= numWorkers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			worker(id, jobs, results, apiURL)
		}(w)
	}

	// Start a goroutine to close the results channel when all workers are done
	go func() {
		wg.Wait()
		close(results)
	}()

	// Start a goroutine to read from stdin and send jobs
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			jobs <- job{message: line}
		}
		close(jobs)
	}()

	// Process results
	for result := range results {
		if result.err != nil {
			fmt.Fprintln(os.Stderr, result.err)
			continue
		}
		fmt.Println(result.response)
	}
} 