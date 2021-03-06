/*
Copyright 2016 Stanislav Liberman

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"flag"
	"fmt"
	"github.com/codahale/hdrhistogram"
	"github.com/lirm/aeron-go/aeron"
	"github.com/lirm/aeron-go/aeron/atomic"
	"github.com/lirm/aeron-go/aeron/logbuffer"
	"github.com/lirm/aeron-go/examples"
	"github.com/op/go-logging"
	"io"
	"os"
	"runtime/pprof"
	"time"
)

var logger = logging.MustGetLogger("examples")

func main() {

	flag.Parse()

	if !*examples.ExamplesConfig.LoggingOn {
		logging.SetLevel(logging.INFO, "aeron")
		logging.SetLevel(logging.INFO, "memmap")
		logging.SetLevel(logging.INFO, "driver")
		logging.SetLevel(logging.INFO, "counters")
		logging.SetLevel(logging.INFO, "logbuffers")
		logging.SetLevel(logging.INFO, "buffer")
		logging.SetLevel(logging.INFO, "examples")
	}

	to := time.Duration(time.Millisecond.Nanoseconds() * *examples.ExamplesConfig.DriverTo)
	ctx := aeron.NewContext().AeronDir(*examples.ExamplesConfig.AeronPrefix).MediaDriverTimeout(to)

	a := aeron.Connect(ctx)

	subscription := <-a.AddSubscription(*examples.PingPongConfig.PongChannel, int32(*examples.PingPongConfig.PongStreamID))
	defer subscription.Close()
	logger.Infof("Subscription found %v", subscription)

	publication := <-a.AddPublication(*examples.PingPongConfig.PingChannel, int32(*examples.PingPongConfig.PingStreamID))
	defer publication.Close()
	logger.Infof("Publication found %v", publication)

	if *examples.ExamplesConfig.ProfilerEnabled {
		fname := fmt.Sprintf("ping-%d.pprof", time.Now().Unix())
		logger.Infof("Profiling enabled. Will use: %s", fname)
		f, err := os.Create(fname)
		if err == nil {
			pprof.StartCPUProfile(f)
			defer pprof.StopCPUProfile()
		} else {
			logger.Errorf("Failed to create profile file with %v", err)
		}
	}

	hist := hdrhistogram.New(1, 1000000000, 3)

	handler := func(buffer *atomic.Buffer, offset int32, length int32, header *logbuffer.Header) {
		sent := buffer.GetInt64(offset)
		now := time.Now().UnixNano()

		hist.RecordValue(now - sent)

		if logger.IsEnabledFor(logging.DEBUG) {
			logger.Debugf("Received message at offset %d, length %d, position %d, termId %d, frame len %d",
				offset, length, header.Offset(), header.TermId(), header.FrameLength())
		}
	}

	srcBuffer := atomic.MakeBuffer(make([]byte, *examples.ExamplesConfig.Size))

	warmupIt := 1000
	logger.Infof("Sending %d messages of %d bytes for warmup", warmupIt, srcBuffer.Capacity())
	for i := 0; i < warmupIt; i++ {
		now := time.Now().UnixNano()
		srcBuffer.PutInt64(0, now)

		for true {
			ret := publication.Offer(srcBuffer, 0, srcBuffer.Capacity(), nil)
			if logger.IsEnabledFor(logging.DEBUG) {
				logger.Debugf("Failed offer %d", ret)
			}
			if ret > 0 {
				break
			}
		}

		for true {
			ret := subscription.Poll(handler, 10)
			if logger.IsEnabledFor(logging.DEBUG) {
				if ret < 0 {
					logger.Debugf("Poll returned %d", ret)
				}
			}
			if ret > 0 {
				break
			}
		}
	}
	hist.Reset()

	logger.Infof("Sending %d messages of %d bytes", *examples.ExamplesConfig.Messages, srcBuffer.Capacity())
	for i := 0; i < *examples.ExamplesConfig.Messages; i++ {
		now := time.Now().UnixNano()
		srcBuffer.PutInt64(0, now)

		for publication.Offer(srcBuffer, 0, srcBuffer.Capacity(), nil) < 0 {
		}

		for subscription.Poll(handler, 10) <= 0 {
		}
	}

	OutputPercentileDistribution(os.Stdout, hist, 1000.0)
}

func OutputPercentileDistribution(out io.Writer, h *hdrhistogram.Histogram, scalingFactor float64) {

	fmt.Print("Value     Percentile TotalCount 1/(1-Percentile)\n\n")

	//Value     Percentile TotalCount 1/(1-Percentile)
	for _, b := range h.CumulativeDistribution() {
		pct := b.Quantile / 100.0
		io.WriteString(out, fmt.Sprintf("%12.3f %2.12f  %10d  %14.2f\n",
			float64(b.ValueAt)/scalingFactor,
			pct,
			b.Count,
			1/(1-pct)))
	}

	io.WriteString(out, fmt.Sprintf("#[Mean    = %12.3f, StdDeviation   = %12.3f]\n",
		h.Mean()/scalingFactor,
		h.StdDev()/scalingFactor))
	io.WriteString(out, fmt.Sprintf("#[Max     = %12.3f, Total count    = %12d]\n",
		float64(h.Max())/scalingFactor,
		h.TotalCount()))
}
