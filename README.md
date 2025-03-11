# Retail Store Image Processing Service

## Description
A service that processes thousands of images from retail stores via a REST API. Users submit jobs with image URLs and store IDs, and the service downloads images, calculates perimeters, and provides status updates.

## Key Architecture: Producer-Consumer Model
- **Producer Thread Pool (10 workers)**: Downloads images from URLs
- **Consumer Thread Pool (5 workers)**: Processes downloaded images, calculates perimeters
- **Benefits**: Optimized resource usage, controlled concurrency, clean separation of concerns

```go
// Thread Pool structure for efficient concurrent processing
type ThreadPool struct {
    wg         sync.WaitGroup
    tasks      chan interface{}
    numWorkers int
}
```

## Core Components
- Job Management System for tracking status and results
- Thread Pool implementation for concurrent processing
- Store Master integration (CSV database)
- REST API endpoints (Gin framework)

## Setup Instructions

### ENV
```
UBUNTU 22.04 LTS
```
### Without Docker
```
go run main.go
```

### With Docker
```
docker-compose up -d --build
```

## Postman Documentation

https://documenter.getpostman.com/view/28829272/2sAYk7TPt3

## Future Improvements

1. **Store CSV in AWS S3**: Allow admin modifications from a single location

2. **Use Snowflake ID Generator**: Eliminate lock contention, handle up to 4000 requests/ms

3. **Performance & Scalability**:
   - Fine Tune thread pool sizing
   - Job Expiry if taking too much time
