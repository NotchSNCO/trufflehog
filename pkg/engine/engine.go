package engine

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/trufflesecurity/trufflehog/pkg/decoders"
	"github.com/trufflesecurity/trufflehog/pkg/detectors"
	"github.com/trufflesecurity/trufflehog/pkg/sources"
)

type Engine struct {
	concurrency int
	chunks      chan *sources.Chunk
	results     chan detectors.ResultWithMetadata
	decoders    []decoders.Decoder
	detectors   map[bool][]detectors.Detector
}

type EngineOption func(*Engine)

func WithConcurrency(concurrency int) EngineOption {
	return func(e *Engine) {
		e.concurrency = concurrency
	}
}

func WithDetectors(verify bool, d ...detectors.Detector) EngineOption {
	return func(e *Engine) {
		if e.detectors == nil {
			e.detectors = make(map[bool][]detectors.Detector)
		}
		if e.detectors[verify] == nil {
			e.detectors[verify] = []detectors.Detector{}
		}
		e.detectors[verify] = append(e.detectors[verify], d...)
	}
}

func WithDecoders(decoders ...decoders.Decoder) EngineOption {
	return func(e *Engine) {
		e.decoders = decoders
	}
}

func Start(ctx context.Context, options ...EngineOption) *Engine {
	e := &Engine{
		chunks:  make(chan *sources.Chunk),
		results: make(chan detectors.ResultWithMetadata),
	}

	for _, option := range options {
		option(e)
	}

	// set defaults

	if e.concurrency == 0 {
		numCPU := runtime.NumCPU()
		logrus.Warn("No concurrency specified, defaulting to ", numCPU)
		e.concurrency = numCPU
	}

	var workerWg sync.WaitGroup

	for i := 0; i < e.concurrency; i++ {
		workerWg.Add(1)
		go func() {
			e.detectorWorker(ctx)
			workerWg.Done()
		}()
	}

	go func() {
		// close results chan when all workers are done
		workerWg.Wait()
		// not entirely sure why results don't get processed without this pause
		// since we've put all results on the channel at this point.
		time.Sleep(time.Second)
		close(e.ResultsChan())
	}()

	if len(e.decoders) == 0 {
		e.decoders = decoders.DefaultDecoders()
	}

	if len(e.detectors) == 0 {
		e.detectors = map[bool][]detectors.Detector{}
		e.detectors[true] = DefaultDetectors()
	}

	return e
}

func (e *Engine) ChunksChan() chan *sources.Chunk {
	return e.chunks
}

func (e *Engine) ResultsChan() chan detectors.ResultWithMetadata {
	return e.results
}

func (e *Engine) detectorWorker(ctx context.Context) {
	for chunk := range e.chunks {
		for _, decoder := range e.decoders {
			decoded := decoder.FromChunk(chunk)
			if decoded == nil {
				continue
			}
			dataLower := strings.ToLower(string(decoded.Data))
			for verify, detectorsSet := range e.detectors {
				for _, detector := range detectorsSet {
					foundKeyword := false
					for _, kw := range detector.Keywords() {
						if strings.Contains(dataLower, strings.ToLower(kw)) {
							foundKeyword = true
							break
						}
					}
					if !foundKeyword {
						continue
					}
					results, err := detector.FromData(ctx, verify, decoded.Data)
					if err != nil {
						logrus.WithError(err).Error("could not scan chunk")
						continue
					}
					for _, result := range results {
						e.results <- detectors.CopyMetadata(chunk, result)
					}
				}
			}
		}
	}
}