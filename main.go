package main

import (
	"encoding/csv"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type Store struct {
	StoreID   string
	StoreName string
	AreaCode  string
}

type Visit struct {
	StoreID   string   `json:"store_id"`
	ImageURLs []string `json:"image_url"`
	VisitTime string   `json:"visit_time"`
}

type SubmitJobRequest struct {
	Count  int     `json:"count"`
	Visits []Visit `json:"visits"`
}

type SubmitJobResponse struct {
	JobID int `json:"job_id"`
}

type JobStatus string

const (
	StatusOngoing   JobStatus = "ongoing"
	StatusCompleted JobStatus = "completed"
	StatusFailed    JobStatus = "failed"
)

type JobStatusResponse struct {
	Status JobStatus  `json:"status"`
	JobID  string     `json:"job_id"`
	Errors []JobError `json:"error,omitempty"`
}

type JobError struct {
	StoreID string `json:"store_id"`
	Error   string `json:"error"`
}

type Job struct {
	ID      int
	Status  JobStatus
	Visits  []Visit
	Errors  []JobError
	Results map[string]map[string]float64
	mu      sync.Mutex
}

type ImageTask struct {
	JobID    int
	StoreID  string
	ImageURL string
}

type ImageResult struct {
	JobID     int
	StoreID   string
	ImageURL  string
	Perimeter float64
	Error     error
	Img       image.Image
}

type ThreadPool struct {
	wg         sync.WaitGroup
	tasks      chan interface{}
	numWorkers int
}

var (
	jobCounter     int
	jobs           = make(map[int]*Job)
	jobsMutex      sync.Mutex
	storeMaster    = make(map[string]Store)
	downloadPool   *ThreadPool
	processingPool *ThreadPool
)

func NewThreadPool(workers int) *ThreadPool {
	pool := &ThreadPool{
		tasks:      make(chan interface{}),
		numWorkers: workers,
	}

	pool.wg.Add(workers)

	return pool
}

func (p *ThreadPool) Start(processor func(interface{})) {
	for i := 0; i < p.numWorkers; i++ {
		go func(workerID int) {
			defer p.wg.Done()
			for task := range p.tasks {
				log.Printf("Worker %d processing task", workerID)
				processor(task)
			}
		}(i)
	}
}

func (p *ThreadPool) AddTask(task interface{}) {
	p.tasks <- task
}

func (p *ThreadPool) Close() {
	close(p.tasks)
	p.wg.Wait()
}

func initializeStoreMaster() {
	file, err := os.Open("storemaster.csv")
	if err != nil {
		log.Fatalf("Failed to open store master file: %v", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		log.Fatalf("Failed to read store master file: %v", err)
	}

	fmt.Println("checking records", records[1])

	for _, record := range records {
		storeMaster[record[2]] = Store{
			StoreID:   record[2],
			StoreName: record[1],
			AreaCode:  record[0],
		}
	}

	// for k, v := range storeMaster {
	// 	fmt.Printf(`storeMaster["%s"] = Store{StoreID: "%s", StoreName: "%s", AreaCode: "%s"}%s`,
	// 		k, v.StoreID, v.StoreName, v.AreaCode, "\n")
	// }

}

func downloadImage(imageTask interface{}) {
	task := imageTask.(ImageTask)

	if _, exists := storeMaster[task.StoreID]; !exists {
		processingPool.AddTask(ImageResult{
			JobID:    task.JobID,
			StoreID:  task.StoreID,
			ImageURL: task.ImageURL,
			Error:    fmt.Errorf("store ID not found"),
		})
		return
	}

	resp, err := http.Get(task.ImageURL)
	if err != nil {
		processingPool.AddTask(ImageResult{
			JobID:    task.JobID,
			StoreID:  task.StoreID,
			ImageURL: task.ImageURL,
			Error:    fmt.Errorf("failed to download image: %v", err),
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		processingPool.AddTask(ImageResult{
			JobID:    task.JobID,
			StoreID:  task.StoreID,
			ImageURL: task.ImageURL,
			Error:    fmt.Errorf("failed to download image: status code %d", resp.StatusCode),
		})
		return
	}

	img, _, err := image.Decode(resp.Body)
	if err != nil {
		processingPool.AddTask(ImageResult{
			JobID:    task.JobID,
			StoreID:  task.StoreID,
			ImageURL: task.ImageURL,
			Error:    fmt.Errorf("failed to decode image: %v", err),
		})
		return
	}

	processingPool.AddTask(ImageResult{
		JobID:     task.JobID,
		StoreID:   task.StoreID,
		ImageURL:  task.ImageURL,
		Perimeter: 0,
		Error:     nil,
		Img:       img,
	})
}

func processImage(result interface{}) {
	imageResult := result.(ImageResult)

	if imageResult.Error != nil {
		updateJobStatus(imageResult)
		return
	}

	bounds := imageResult.Img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	perimeter := 2 * (float64(width) + float64(height))

	sleepTime := 100 + rand.Intn(301)
	time.Sleep(time.Duration(sleepTime) * time.Millisecond)

	imageResult.Perimeter = perimeter
	imageResult.Img = nil

	updateJobStatus(imageResult)
}

func updateJobStatus(result ImageResult) {
	jobsMutex.Lock()
	defer jobsMutex.Unlock()

	job, exists := jobs[result.JobID]
	if !exists {
		return
	}

	job.mu.Lock()
	defer job.mu.Unlock()

	if result.Error != nil {
		job.Errors = append(job.Errors, JobError{
			StoreID: result.StoreID,
			Error:   result.Error.Error(),
		})
		job.Status = StatusFailed
	} else {
		if job.Results == nil {
			job.Results = make(map[string]map[string]float64)
		}
		if job.Results[result.StoreID] == nil {
			job.Results[result.StoreID] = make(map[string]float64)
		}
		job.Results[result.StoreID][result.ImageURL] = result.Perimeter

		totalImages := 0
		processedImages := 0

		for _, visit := range job.Visits {
			totalImages += len(visit.ImageURLs)
			if storeResults, ok := job.Results[visit.StoreID]; ok {
				processedImages += len(storeResults)
			}
		}

		if processedImages == totalImages && job.Status != StatusFailed {
			job.Status = StatusCompleted
		}
	}
}

func handleSubmitJob(c *gin.Context) {
	var req SubmitJobRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Invalid request: %v", err)})
		return
	}

	if req.Count != len(req.Visits) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Count does not match the number of visits"})
		return
	}

	jobsMutex.Lock()
	jobCounter++
	jobID := jobCounter
	job := &Job{
		ID:      jobID,
		Status:  StatusOngoing,
		Visits:  req.Visits,
		Results: make(map[string]map[string]float64),
	}
	jobs[jobID] = job
	jobsMutex.Unlock()

	for _, visit := range req.Visits {
		for _, imageURL := range visit.ImageURLs {
			downloadPool.AddTask(ImageTask{
				JobID:    jobID,
				StoreID:  visit.StoreID,
				ImageURL: imageURL,
			})
		}
	}

	c.JSON(http.StatusCreated, SubmitJobResponse{JobID: jobID})
}

func handleJobStatus(c *gin.Context) {
	jobIDStr := c.Query("jobid")
	if jobIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing job ID"})
		return
	}

	jobID, err := strconv.Atoi(jobIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid job ID"})
		return
	}

	jobsMutex.Lock()
	job, exists := jobs[jobID]
	jobsMutex.Unlock()

	if !exists {
		c.JSON(http.StatusBadRequest, gin.H{})
		return
	}

	job.mu.Lock()
	resp := JobStatusResponse{
		Status: job.Status,
		JobID:  strconv.Itoa(job.ID),
	}

	if job.Status == StatusFailed {
		resp.Errors = job.Errors
	}
	job.mu.Unlock()

	c.JSON(http.StatusOK, resp)
}

func main() {
	initializeStoreMaster()

	downloadPool = NewThreadPool(10)  // 10 workers for downloading
	processingPool = NewThreadPool(5) // 5 workers for processing

	downloadPool.Start(downloadImage)

	processingPool.Start(processImage)

	router := gin.Default()

	router.POST("/api/submit/", handleSubmitJob)
	router.GET("/api/status", handleJobStatus)

	log.Println("Server starting on :8080...")
	if err := router.Run(":8080"); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}

	downloadPool.Close()
	processingPool.Close()
}
