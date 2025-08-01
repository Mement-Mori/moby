package daemon

import (
	"context"

	cerrdefs "github.com/containerd/errdefs"
	containertypes "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/v2/daemon/container"
	"github.com/moby/moby/v2/daemon/internal/platform"
)

func (daemon *Daemon) stats(c *container.Container) (*containertypes.StatsResponse, error) {
	c.Lock()
	task, err := c.GetRunningTask()
	c.Unlock()
	if err != nil {
		return nil, err
	}

	// Obtain the stats from HCS via libcontainerd
	stats, err := task.Stats(context.Background())
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return nil, containerNotFound(c.ID)
		}
		return nil, err
	}

	// Start with an empty structure
	s := &containertypes.StatsResponse{
		Read:     stats.Read,
		NumProcs: platform.NumProcs(),
	}

	if stats.HCSStats != nil {
		hcss := stats.HCSStats
		// Populate the CPU/processor statistics
		s.CPUStats = containertypes.CPUStats{
			CPUUsage: containertypes.CPUUsage{
				TotalUsage:        hcss.Processor.TotalRuntime100ns,
				UsageInKernelmode: hcss.Processor.RuntimeKernel100ns,
				UsageInUsermode:   hcss.Processor.RuntimeUser100ns,
			},
		}

		// Populate the memory statistics
		s.MemoryStats = containertypes.MemoryStats{
			Commit:            hcss.Memory.UsageCommitBytes,
			CommitPeak:        hcss.Memory.UsageCommitPeakBytes,
			PrivateWorkingSet: hcss.Memory.UsagePrivateWorkingSetBytes,
		}

		// Populate the storage statistics
		s.StorageStats = containertypes.StorageStats{
			ReadCountNormalized:  hcss.Storage.ReadCountNormalized,
			ReadSizeBytes:        hcss.Storage.ReadSizeBytes,
			WriteCountNormalized: hcss.Storage.WriteCountNormalized,
			WriteSizeBytes:       hcss.Storage.WriteSizeBytes,
		}

		// Populate the network statistics
		s.Networks = make(map[string]containertypes.NetworkStats)
		for _, nstats := range hcss.Network {
			s.Networks[nstats.EndpointId] = containertypes.NetworkStats{
				RxBytes:   nstats.BytesReceived,
				RxPackets: nstats.PacketsReceived,
				RxDropped: nstats.DroppedPacketsIncoming,
				TxBytes:   nstats.BytesSent,
				TxPackets: nstats.PacketsSent,
				TxDropped: nstats.DroppedPacketsOutgoing,
			}
		}
	}
	return s, nil
}

// Windows network stats are obtained directly through HCS, hence this is a no-op.
func (daemon *Daemon) getNetworkStats(c *container.Container) (map[string]containertypes.NetworkStats, error) {
	return make(map[string]containertypes.NetworkStats), nil
}

// getSystemCPUUsage returns the host system's cpu usage in
// nanoseconds and number of online CPUs. An error is returned
// if the format of the underlying file does not match.
// This is a no-op on Windows.
func getSystemCPUUsage() (uint64, uint32, error) {
	return 0, 0, nil
}
