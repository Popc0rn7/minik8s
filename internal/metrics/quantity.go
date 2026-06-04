package metrics

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

func ParseCPUQuantity(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("cpu quantity is empty")
	}
	if strings.HasSuffix(value, "m") {
		number := strings.TrimSuffix(value, "m")
		milli, err := strconv.ParseFloat(number, 64)
		if err != nil {
			return 0, err
		}
		return int64(math.Round(milli * 1_000_000)), nil
	}
	cpus, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, err
	}
	return int64(math.Round(cpus * 1_000_000_000)), nil
}

func ParseMemoryQuantity(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("memory quantity is empty")
	}
	units := map[string]float64{
		"Ki": 1024,
		"Mi": 1024 * 1024,
		"Gi": 1024 * 1024 * 1024,
		"K":  1000,
		"M":  1000 * 1000,
		"G":  1000 * 1000 * 1000,
	}
	for suffix, multiplier := range units {
		if strings.HasSuffix(value, suffix) {
			number := strings.TrimSuffix(value, suffix)
			amount, err := strconv.ParseFloat(number, 64)
			if err != nil {
				return 0, err
			}
			return int64(math.Round(amount * multiplier)), nil
		}
	}
	return strconv.ParseInt(value, 10, 64)
}
