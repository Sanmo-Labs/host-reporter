package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mdlayher/vsock"
	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/disk"
	"github.com/shirou/gopsutil/mem"
	"github.com/shirou/gopsutil/net"
)

const (
	vsockCID  = uint32(2)
	vsockPort = uint32(5000)
)

var (
	conn      *vsock.Conn
	connMutex sync.Mutex
)

type Monitor struct {
	Timestamp  int64  `json:"timestamp"`
	InstanceID string `json:"instance_id"`

	// Memory Metrics
	TotalRAM     uint64  `json:"total_ram"`
	AvailableRAM uint64  `json:"available_ram"`
	UsedRAM      uint64  `json:"used_ram"`
	UsedPercent  float64 `json:"used_ram_percent"`

	// CPU Metrics
	CPUUsage     float64 `json:"cpu_usage_percent"`
	CPUUsed      float64 `json:"cpu_used"`
	CPUAvailable float64 `json:"cpu_available"`

	// Disk Metrics
	DiskTotal     uint64  `json:"disk_total"`
	DiskUsed      uint64  `json:"disk_used"`
	DiskAvailable uint64  `json:"disk_available"`
	DiskUsage     float64 `json:"disk_usage_percent"`

	// Network Metrics
	BytesSent         uint64  `json:"bytes_sent"`
	BytesReceived     uint64  `json:"bytes_received"`
	BandwidthSent     float64 `json:"bandwidth_sent"`
	BandwidthReceived float64 `json:"bandwidth_received"`
}

func round(value float64) float64 {
	return math.Round(value*100) / 100
}

func (m *Monitor) fetchInstanceID() {
	data, err := os.ReadFile("/var/lib/cloud/data/instance-id")
	if err != nil {
		log.Fatalf("Error reading instance ID: %v", err)
	}

	m.InstanceID = strings.TrimSpace(string(data))
}

func (m *Monitor) updateMemory() {
	v, err := mem.VirtualMemory()
	if err == nil {
		m.TotalRAM = v.Total
		m.AvailableRAM = v.Available
		m.UsedRAM = v.Used
		m.UsedPercent = round(v.UsedPercent)
	} else {
		log.Println("Error fetching memory stats:", err)
	}
}

func (m *Monitor) updateCPU() {
	cpuPercent, err := cpu.Percent(time.Second, false)
	if err == nil && len(cpuPercent) > 0 {
		m.CPUUsage = round(cpuPercent[0])
		totalCPU := float64(runtime.NumCPU())
		m.CPUUsed = round((m.CPUUsage / 100) * totalCPU)
		m.CPUAvailable = round(totalCPU - m.CPUUsed)
	} else {
		log.Println("Error fetching CPU stats:", err)
	}
}

func (m *Monitor) updateDisk() {
	diskStat, err := disk.Usage("/")
	if err == nil {
		m.DiskTotal = diskStat.Total
		m.DiskUsed = diskStat.Used
		m.DiskAvailable = diskStat.Free
		m.DiskUsage = round(diskStat.UsedPercent)
	} else {
		log.Println("Error fetching disk stats:", err)
	}
}

func (m *Monitor) updateNetwork(prevBytesSent, prevBytesReceived uint64, elapsedTime time.Duration) (uint64, uint64) {
	netStats, err := net.IOCounters(false)
	if err == nil && len(netStats) > 0 {
		currentBytesSent := netStats[0].BytesSent
		currentBytesReceived := netStats[0].BytesRecv

		// Calculate bandwidth (bytes per second)
		m.BandwidthSent = round(float64(currentBytesSent-prevBytesSent) / elapsedTime.Seconds())
		m.BandwidthReceived = round(float64(currentBytesReceived-prevBytesReceived) / elapsedTime.Seconds())

		// Update total bytes sent/received
		m.BytesSent = currentBytesSent
		m.BytesReceived = currentBytesReceived

		return currentBytesSent, currentBytesReceived
	}
	log.Println("Error fetching network stats:", err)
	return prevBytesSent, prevBytesReceived
}

func (m *Monitor) sendMetrics() error {
	connMutex.Lock()
	defer connMutex.Unlock()

	if conn == nil {
		return fmt.Errorf("connection is nil")
	}

	metricsJSON, err := json.Marshal(m)
	if err != nil {
		return err
	}

	_, err = conn.Write(metricsJSON)
	if err != nil {
		return err
	}

	return nil
}

func reConnect(vsockCID uint32, vsockPort uint32) {
	connMutex.Lock()
	defer connMutex.Unlock()

	if conn != nil {
		conn.Close()
	}

	newConn, err := vsock.Dial(vsockCID, vsockPort, nil)
	if err != nil {
		log.Printf("Error reconnecting to vsock: %v\n", err)
		return
	}

	conn = newConn
	log.Println("Reconnected to vsock successfully.")
}

func (m *Monitor) Update(interval time.Duration, vsockCID uint32, vsockPort uint32) {
	m.fetchInstanceID()

	var prevBytesSent, prevBytesReceived uint64
	startTime := time.Now()

	for {
		m.Timestamp = time.Now().UnixMilli()
		m.updateMemory()
		m.updateCPU()
		m.updateDisk()

		elapsedTime := time.Since(startTime)
		prevBytesSent, prevBytesReceived = m.updateNetwork(prevBytesSent, prevBytesReceived, elapsedTime)
		startTime = time.Now()

		fmt.Println("Sending metrics...")
		go func() {
			if err := m.sendMetrics(); err != nil {
				log.Println("Error sending metrics:", err)
				reConnect(vsockCID, vsockPort)
			}
		}()

		fmt.Println("Metrics sent...")
		time.Sleep(interval)
	}
}

func main() {
	interval := 10 * time.Second

	if envInterval := os.Getenv("MONITOR_INTERVAL"); envInterval != "" {
		if parsedInterval, err := time.ParseDuration(envInterval); err == nil {
			interval = parsedInterval
		} else {
			log.Printf("Invalid MONITOR_INTERVAL value: %v. Using default interval: %v\n", envInterval, interval)
		}
	}

	var err error

	conn, err = vsock.Dial(vsockCID, vsockPort, nil)
	for err != nil {
		log.Printf("error connecting to vsock: %v. Retrying...\n", err)

		time.Sleep(5 * time.Second)

		conn, err = vsock.Dial(vsockCID, vsockPort, nil)
	}

	defer func() {
		connMutex.Lock()
		if conn != nil {
			conn.Close()
		}
		connMutex.Unlock()
	}()

	monitor := &Monitor{}
	go monitor.Update(interval, vsockCID, vsockPort)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Println("Shutting down...")
}
