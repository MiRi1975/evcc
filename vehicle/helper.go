package vehicle

import (
	"fmt"
	"strings"

	"github.com/thoas/go-funk"
)

// findVehicle finds the first vehicle in the list of VINs or returns an error
func findVehicle(vehicles []string, err error) (string, error) {
	if err != nil {
		return "", fmt.Errorf("cannot get vehicles: %w", err)
	}

	if len(vehicles) != 1 {
		return "", fmt.Errorf("cannot find vehicle: %v", vehicles)
	}

	vin := strings.TrimSpace(vehicles[0])
	if vin == "" {
		return "", fmt.Errorf("cannot find vehicle: %v", vehicles)
	}

	return vin, nil
}

// ensureVehicle ensures that the vehicle is available on the api and returns the VIN
func ensureVehicle(vin string, fun func() ([]string, error)) (string, error) {
	vehicles, err := fun()
	if err != nil {
		return "", fmt.Errorf("cannot get vehicles: %w", err)
	}

	if vin = strings.ToUpper(vin); vin != "" {
		// vin defined but doesn't exist
		if !funk.ContainsString(vehicles, vin) {
			err = fmt.Errorf("cannot find vehicle: %s", vin)
		}
	} else {
		// vin empty
		vin, err = findVehicle(vehicles, nil)
	}

	return vin, err
}

// ensureVehicle2 is the simple wrapper around the generic version
func ensureVehicle2(vin string, fun func() ([]string, error)) (string, error) {
	vin, _, err := ensureVehicleGen[string, string](vin, fun, func(v string) (string, string) {
		return v, v
	})

	return vin, err
}

// ensureVehicleGen is the generic version of ensureVehicle
func ensureVehicleGen[T, R any](
	vin string,
	list func() ([]T, error),
	extract func(T) (string, R),
) (string, R, error) {
	vehicles, err := list()
	if err != nil {
		return "", *new(R), fmt.Errorf("cannot get vehicles: %w", err)
	}

	if vin = strings.ToUpper(vin); vin != "" {
		// vin defined but doesn't exist
		for _, vehicle := range vehicles {
			if vin2, res := extract(vehicle); vin2 == vin {
				return vin2, res, nil
			}
		}

		err = fmt.Errorf("cannot find vehicle: %s", vin)
	} else {
		// vin empty
		if len(vehicles) == 1 {
			vin, res := extract(vehicles[0])
			return vin, res, nil
		}

		err = fmt.Errorf("cannot find vehicle: %v", vehicles)
	}

	return "", *new(R), err
}
