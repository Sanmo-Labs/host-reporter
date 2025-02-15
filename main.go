package main

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/shirou/gopsutil/mem"
)

type Monitor struct {
	Config       Config
	TotalRAM     uint64
	AvailableRAM uint64
	UsedRAM      uint64
	UsedPercent  float64
	CanProvision bool
	mutex        sync.Mutex
}

func (m *Monitor) Update() {
	for {
		v, err := mem.VirtualMemory()
		if err != nil {
			fmt.Println("Error fetching memory stats:", err)
			continue
		}

		reserved := (v.Total * uint64(m.Config.ReservedRAMPct)) / 100
		m.mutex.Lock()
		m.TotalRAM = v.Total
		m.AvailableRAM = v.Available
		m.UsedRAM = v.Used
		m.UsedPercent = v.UsedPercent
		m.CanProvision = v.Available > reserved
		m.mutex.Unlock()

		memJSON, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			fmt.Println("Error encoding JSON:", err)
			continue
		}

		fmt.Println("Memory Usage Report:\n", string(memJSON))
		time.Sleep(time.Duration(m.Config.CheckInterval) * time.Second)
	}
}

func main() {
	config, err := ReadConfig("/etc/sanmo_config.yaml")
	if err != nil {
		fmt.Println("Error loading config:", err)
		return
	}

	monitor := &Monitor{Config: config}
	go monitor.Update()

	select {}
}
