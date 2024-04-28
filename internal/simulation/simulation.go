package simulation

import (
	"context"
	"crypto/rand"
	"fmt"
	"github.com/go-logr/logr"
	"io"
	rand2 "math/rand"
	"os"
	runtime2 "runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"time"
)

func RunCPUWorkload(duration int) {
	// Run any CPU workload
	done := make(chan int)

	for i := 0; i < runtime2.NumCPU(); i++ {
		go func() {
			for {
				select {
				case <-done:
					return
				default:
				}
			}
		}()
	}

	time.Sleep(time.Duration(duration) * time.Second)
	close(done)
}

func RunMemoryLoad(duration int, megabytes int) {
	type MemoryLoad struct {
		Block []byte
	}

	numBlocks := megabytes   // Adjust this value depending on how much memory you want to consume.
	blockSize := 1024 * 1024 // Each block is 1 megabyte.

	var blocks = make([]MemoryLoad, numBlocks)

	for i := 0; i < numBlocks; i++ {
		blocks[i] = MemoryLoad{make([]byte, blockSize)}

		if i%100 == 0 {
			fmt.Printf("Allocated %v MB\n", (i+1)*blockSize/(1024*1024))
		}
	}

	fmt.Println("Holding memory for ", duration, " seconds...")
	time.Sleep(time.Second * time.Duration(duration))
	fmt.Println("Memory sleep finished")
	runtime2.GC()
}

// SimulateIO simulates file I/O operations by writing and reading back random data from a temporary file.
// `duration` specifies how long the I/O operations should run, and `sizeMB` specifies the size of the data to write (in each operation in megabytes)
func SimulateIO(ctx context.Context, duration int, sizeMB int) {
	logger := log.FromContext(ctx)
	logger.Info("Starting I/O simulation", "Duration", duration, "SizeMB", sizeMB)

	// Define the duration for the simulation
	endTime := time.Now().Add(time.Duration(duration) * time.Second)
	randomSource := rand2.New(rand2.NewSource(time.Now().UnixNano()))

	for time.Now().Before(endTime) {
		fileSize := randomSource.Intn(sizeMB) + 1 // Ensure non-zero file size
		data := make([]byte, fileSize*1024*1024)
		_, err := rand.Read(data)
		if err != nil {
			logger.Error(err, "Failed to generate random data")
		}

		// Simulate mixed read/write and random access by creating multiple files
		for i := 0; i < 5; i++ { // Create multiple files to simulate handling multiple resources
			fileName := fmt.Sprintf("simulate-io-%d", i)
			err := performFileOperations(ctx, fileName, data)
			if err != nil {
				logger.Error(err, "Failed to perform file operations")
			}
		}
	}

	runtime2.GC() // Force garbage collection to clean up memory

	logger.Info("Complex I/O simulation completed")
}

// performFileOperations handles the actual file writing and reading, simulating random access and mixed operations.
func performFileOperations(ctx context.Context, fileName string, data []byte) error {
	logger := log.FromContext(ctx)
	// Create a temporary file
	tmpfile, err := os.CreateTemp("", fileName)
	if err != nil {
		logger.Error(err, "Failed to create temporary file")
		return err
	}
	defer func(name string) {
		err := os.Remove(name)
		if err != nil {
			logger.Error(err, "Failed to remove temporary file")
		}
	}(tmpfile.Name()) // Clean up

	// Randomly choose to write or read first to simulate mixed operations
	if rand2.Int()%2 == 0 {
		if err := writeAndReadFile(tmpfile, data, logger); err != nil {
			return err
		}
	} else {
		if err := readFileAndWrite(tmpfile, data, logger); err != nil {
			return err
		}
	}

	return nil
}

func writeAndReadFile(file *os.File, data []byte, logger logr.Logger) error {
	defer file.Close()

	if _, err := file.Write(data); err != nil {
		logger.Error(err, "Failed to write to file")
		return err
	}

	if _, err := file.Seek(0, io.SeekStart); err != nil {
		logger.Error(err, "Failed to seek in file")
		return err
	}

	if _, err := io.ReadAll(file); err != nil {
		logger.Error(err, "Failed to read from file")
		return err
	}

	return nil
}

func readFileAndWrite(file *os.File, data []byte, logger logr.Logger) error {
	defer file.Close()

	if _, err := io.ReadAll(file); err != nil {
		logger.Error(err, "Failed to read from file")
		return err
	}

	if _, err := file.Write(data); err != nil {
		logger.Error(err, "Failed to write to file")
		return err
	}

	return nil
}
