package migrators

import (
	k8smetrics "k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
)

const (
	namespace = "storage_migrator"
	subsystem = "core_migrator"
)

var (
	// metrics provides access to all core migrator metrics.
	metrics = newMigratorMetrics()
)

// migratorMetrics instruments core migrator with prometheus metrics.
type migratorMetrics struct {
	objectsMigrated *k8smetrics.CounterVec
	migration       *k8smetrics.CounterVec
}

// newMigratorMetrics create a new MigratorMetrics, configured with default metric names.
func newMigratorMetrics() *migratorMetrics {
	objectsMigrated := k8smetrics.NewCounterVec(
		&k8smetrics.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "migrated_objects",
			Help:      "The number of objects that have been migrated, labeled with the full resource name.",
		}, []string{"resource"})
	legacyregistry.Register(objectsMigrated)

	migration := k8smetrics.NewCounterVec(
		&k8smetrics.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "migrations",
			Help:      "The number of completed migration, labeled with the full resource name, and the status of the migration (failed or succeeded)",
		}, []string{"resource", "status"})
	legacyregistry.Register(migration)

	return &migratorMetrics{
		objectsMigrated: objectsMigrated,
		migration:       migration,
	}
}

func (m *migratorMetrics) Reset() {
	m.objectsMigrated.Reset()
	m.migration.Reset()
}

// ObserveObjectsMigrated adds the number of migrated objects for a resource type..
func (m *migratorMetrics) ObserveObjectsMigrated(added int, resource string) {
	m.objectsMigrated.WithLabelValues(resource).Add(float64(added))
}

// ObserveSucceededMigration increments the number of successful migrations for a resource type..
func (m *migratorMetrics) ObserveSucceededMigration(resource string) {
	m.migration.WithLabelValues(resource, "Succeeded").Add(float64(1))
}

// ObserveFailedMigration increments the number of failed migrations for a resource type..
func (m *migratorMetrics) ObserveFailedMigration(resource string) {
	m.migration.WithLabelValues(resource, "Failed").Add(float64(1))
}
