package provider

import "fmt"

type Registry struct {
	drivers map[Kind]Driver
}

func NewRegistry(drivers ...Driver) (*Registry, error) {
	registry := &Registry{drivers: make(map[Kind]Driver, len(drivers))}
	for _, driver := range drivers {
		if driver == nil {
			return nil, ErrInvalid
		}
		kind := driver.Kind()
		if kind == "" {
			return nil, ErrInvalid
		}
		if _, exists := registry.drivers[kind]; exists {
			return nil, fmt.Errorf("%s: %w", kind, ErrDriverAlreadyExists)
		}
		registry.drivers[kind] = driver
	}
	return registry, nil
}

// NewPhaseOneRegistry intentionally installs exactly one real provider kind.
func NewPhaseOneRegistry(discoverer HTTPAssetDiscoverer) (*Registry, error) {
	driver, err := NewHTTPOpenAPIDriver(discoverer)
	if err != nil {
		return nil, err
	}
	return NewRegistry(driver)
}

func (r *Registry) Resolve(kind Kind) (Driver, error) {
	if r == nil {
		return nil, ErrKindNotAvailable
	}
	driver, exists := r.drivers[kind]
	if !exists {
		return nil, fmt.Errorf("%s: %w", kind, ErrKindNotAvailable)
	}
	return driver, nil
}
