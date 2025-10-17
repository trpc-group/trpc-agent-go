package metric

import (
	"path/filepath"
)

// defaultMetricsFileSuffix is the default suffix for metric files.
const defaultMetricsFileSuffix = ".metrics.json"

// Locator defines the interface for locating metric files.
type Locator interface {
	// Build builds the path of a metric file identified by the given app name and eval set ID.
	Build(baseDir, appName, evalSetID string) string
}

// locator is the default Locator implementation.
type locator struct{}

// Build builds the path of a metric file.
func (l *locator) Build(baseDir, appName, evalSetID string) string {
	return filepath.Join(baseDir, appName, evalSetID+defaultMetricsFileSuffix)
}
