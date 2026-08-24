package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"log"
	"math/rand"
	"os"
	"sync"
	"sync/atomic"
	"time"

	pb "github.com/BuzzingTaz/fw-edge-apps/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type StreamConfig struct {
	StreamID     string `json:"stream_id"`
	VideoPath    string `json:"video_path"`
	AssignedNode string `json:"assigned_node"`
}

type Config struct {
	SchedulerAddr string         `json:"scheduler_addr"`
	Streams       []StreamConfig `json:"streams"`
}

var (
	configPath = flag.String("config", "benchmark_config.json", "Path to benchmark config file")
	duration   = flag.Int("duration", 60, "Benchmark duration in seconds")
	fps        = flag.Float64("fps", 30.0, "Target FPS per stream (e.g. 4.0 for 80 FPS total across 20 streams)")
)

// Statistics tracking
var (
	totalSent       int64
	totalRecv       int64
	totalDropped    int64
	latencySumMs    int64
	latencyCount    int64
	maxLatencyMs    int64
)

func generateMockJPEG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 640, 480))
	// Draw a solid dark background with a random colored circle to simulate movement
	bgColor := color.RGBA{30, 30, 30, 255}
	for x := 0; x < 640; x++ {
		for y := 0; y < 480; y++ {
			img.Set(x, y, bgColor)
		}
	}

	// Draw simulated object
	circleColor := color.RGBA{
		uint8(100 + rand.Intn(155)),
		uint8(100 + rand.Intn(155)),
		uint8(100 + rand.Intn(155)),
		255,
	}
	cx, cy, r := 320+rand.Intn(100)-50, 240+rand.Intn(100)-50, 40
	for x := cx - r; x <= cx+r; x++ {
		for y := cy - r; y <= cy+r; y++ {
			if (x-cx)*(x-cx)+(y-cy)*(y-cy) <= r*r {
				img.Set(x, y, circleColor)
			}
		}
	}

	var buf bytes.Buffer
	jpeg.Encode(&buf, img, &jpeg.Options{Quality: 75})
	return buf.Bytes()
}

func runStream(ctx context.Context, client pb.SchedulerStreamClient, streamConf StreamConfig, jpegData []byte, wg *sync.WaitGroup) {
	defer wg.Done()

	stream, err := client.StreamFrames(ctx)
	if err != nil {
		log.Printf("[%s] Failed to establish gRPC stream: %v", streamConf.StreamID, err)
		return
	}
	defer stream.CloseSend()

	log.Printf("[%s] Started streaming (pinned to %s)...", streamConf.StreamID, streamConf.AssignedNode)

	// Receiver loop
	go func() {
		for {
			res, err := stream.Recv()
			if err == io.EOF {
				return
			}
			if err != nil {
				// Don't log read errors if context is cancelled
				if ctx.Err() == nil {
					log.Printf("[%s] Stream recv error: %v", streamConf.StreamID, err)
				}
				return
			}

			atomic.AddInt64(&totalRecv, 1)
			if res.StageTs != nil && res.StageTs.ClientCaptureMs > 0 {
				e2e := time.Now().UnixMilli() - res.StageTs.ClientCaptureMs
				atomic.AddInt64(&latencySumMs, e2e)
				atomic.AddInt64(&latencyCount, 1)

				for {
					currMax := atomic.LoadInt64(&maxLatencyMs)
					if e2e <= currMax || atomic.CompareAndSwapInt64(&maxLatencyMs, currMax, e2e) {
						break
					}
				}
			}
		}
	}()

	// Sender loop (configurable target FPS per stream)
	targetFPS := *fps
	if targetFPS <= 0 {
		targetFPS = 30.0
	}
	interval := time.Duration(float64(time.Second) / targetFPS)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	frameCounter := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			frameCounter++
			frameID := fmt.Sprintf("%s-frame-%d-%d", streamConf.StreamID, frameCounter, time.Now().UnixNano())
			captureTime := time.Now().UnixMilli()

			frame := &pb.ClientFrame{
				JpegData:           jpegData,
				StreamId:           streamConf.StreamID,
				FrameId:            frameID,
				CaptureTimestampMs: captureTime,
				StageTs: &pb.StageTimestamps{
					ClientCaptureMs: captureTime,
				},
			}

			if err := stream.Send(frame); err != nil {
				log.Printf("[%s] Send failed: %v", streamConf.StreamID, err)
				atomic.AddInt64(&totalDropped, 1)
				return
			}
			atomic.AddInt64(&totalSent, 1)
		}
	}
}

func main() {
	flag.Parse()

	// Load configuration
	data, err := os.ReadFile(*configPath)
	if err != nil {
		log.Fatalf("Failed to read config file: %v", err)
	}

	var conf Config
	if err := json.Unmarshal(data, &conf); err != nil {
		log.Fatalf("Failed to parse config JSON: %v", err)
	}

	log.Printf("Starting benchmark dials to %s with %d streams...", conf.SchedulerAddr, len(conf.Streams))

	// Establish global gRPC connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	conn, err := grpc.DialContext(ctx, conf.SchedulerAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	cancel()
	if err != nil {
		log.Fatalf("Failed to connect to scheduler gRPC server: %v", err)
	}
	defer conn.Close()

	client := pb.NewSchedulerStreamClient(conn)

	// Pre-generate a mock JPEG frame to avoid CPU overhead during run
	mockJPEG := generateMockJPEG()
	log.Printf("Generated mock JPEG payload size: %d bytes", len(mockJPEG))

	runCtx, runCancel := context.WithTimeout(context.Background(), time.Duration(*duration)*time.Second)
	defer runCancel()

	var wg sync.WaitGroup
	for _, streamConf := range conf.Streams {
		wg.Add(1)
		go runStream(runCtx, client, streamConf, mockJPEG, &wg)
	}

	// Statistics reporter
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		var lastSent int64
		var lastRecv int64
		lastTime := time.Now()

		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				now := time.Now()
				elapsed := now.Sub(lastTime).Seconds()
				lastTime = now

				sent := atomic.LoadInt64(&totalSent)
				recv := atomic.LoadInt64(&totalRecv)
				dropped := atomic.LoadInt64(&totalDropped)
				latSum := atomic.LoadInt64(&latencySumMs)
				latCount := atomic.LoadInt64(&latencyCount)
				maxLat := atomic.SwapInt64(&maxLatencyMs, 0) // Reset max latency window

				sendFPS := float64(sent-lastSent) / elapsed
				recvFPS := float64(recv-lastRecv) / elapsed
				lastSent = sent
				lastRecv = recv

				avgLat := 0.0
				if latCount > 0 {
					avgLat = float64(latSum) / float64(latCount)
				}

				fmt.Printf("[BENCHMARK] Elapsed: %.1fs | Sent: %d (%.1f fps) | Recv: %d (%.1f fps) | Dropped: %d | Avg Latency: %.1fms | Max Window Latency: %dms\n",
					elapsed, sent, sendFPS, recv, recvFPS, dropped, avgLat, maxLat)
			}
		}
	}()

	wg.Wait()
	log.Println("Benchmark run completed successfully.")
}
