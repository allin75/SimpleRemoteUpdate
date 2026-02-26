package main

import (
	"fmt"
	"strconv"
	"strings"
)

func isValidVersion(v string) bool {
	_, _, _, err := parseVersion(v)
	return err == nil
}

func normalizeVersion(v string) string {
	return strings.TrimSpace(v)
}

func nextPatchVersion(current string) (string, error) {
	major, minor, patch, err := parseVersion(current)
	if err != nil {
		return "", err
	}
	patch++
	return fmt.Sprintf("%d.%d.%d", major, minor, patch), nil
}

func parseVersion(v string) (int, int, int, error) {
	v = strings.TrimSpace(v)
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return 0, 0, 0, fmt.Errorf("版本号格式错误: %s", v)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil || major < 0 {
		return 0, 0, 0, fmt.Errorf("版本号格式错误: %s", v)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil || minor < 0 {
		return 0, 0, 0, fmt.Errorf("版本号格式错误: %s", v)
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil || patch < 0 {
		return 0, 0, 0, fmt.Errorf("版本号格式错误: %s", v)
	}
	return major, minor, patch, nil
}

