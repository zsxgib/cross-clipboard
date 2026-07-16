package ui

import "fmt"

// humanReadableSize converts a byte count to a human-readable string.
func humanReadableSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	suffix := "KMGTPE"
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), suffix[exp])
}

// humanReadableSpeed converts bytes-per-second to a human-readable string.
func humanReadableSpeed(bps int64) string {
	return humanReadableSize(bps) + "/s"
}

// progressBar renders a 10-cell progress bar like [████░░░░░░] 42%.
func progressBar(sent, total int64) string {
	if total <= 0 {
		return "[]"
	}
	pct := int(float64(sent) / float64(total) * 100)
	if pct > 100 {
		pct = 100
	}
	const width = 10
	filled := pct * width / 100
	bar := ""
	for i := 0; i < width; i++ {
		if i < filled {
			bar += "█"
		} else {
			bar += "░"
		}
	}
	return fmt.Sprintf("[%s] %d%%", bar, pct)
}
