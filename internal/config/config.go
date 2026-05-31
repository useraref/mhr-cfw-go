package config

import (
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
)

type Config map[string]any

func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	return Config(cfg), nil
}

func (c Config) Set(key string, value any) {
	c[key] = value
}

func (c Config) GetString(key, def string) string {
	if v, ok := c[key]; ok {
		switch t := v.(type) {
		case string:
			return t
		case []byte:
			return string(t)
		case float64:
			return strconv.FormatInt(int64(t), 10)
		case int:
			return strconv.Itoa(t)
		case bool:
			if t {
				return "true"
			}
			return "false"
		}
	}
	return def
}

func (c Config) GetInt(key string, def int) int {
	if v, ok := c[key]; ok {
		switch t := v.(type) {
		case float64:
			return int(t)
		case int:
			return t
		case string:
			if i, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
				return i
			}
		}
	}
	return def
}

func (c Config) GetBool(key string, def bool) bool {
	if v, ok := c[key]; ok {
		switch t := v.(type) {
		case bool:
			return t
		case string:
			s := strings.TrimSpace(strings.ToLower(t))
			return s == "1" || s == "true" || s == "yes" || s == "y"
		case float64:
			return t != 0
		}
	}
	return def
}

func (c Config) GetStringSlice(key string) []string {
	v, ok := c[key]
	if !ok {
		return nil
	}
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return nil
		}
		return []string{s}
	}
	return nil
}

func (c Config) GetStringMap(key string) map[string]string {
	out := map[string]string{}
	v, ok := c[key]
	if !ok {
		return out
	}
	switch t := v.(type) {
	case map[string]string:
		return t
	case map[string]any:
		for k, v := range t {
			if s, ok := v.(string); ok {
				out[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(s)
			}
		}
	}
	return out
}

func (c Config) GetScriptIDs() []string {
	ids := c.GetStringSlice("script_ids")
	if len(ids) > 0 {
		return ids
	}
	if s := c.GetString("script_id", ""); s != "" {
		return []string{s}
	}
	return nil
}

func (c Config) GetScriptID() string {
	ids := c.GetScriptIDs()
	if len(ids) > 0 {
		return ids[0]
	}
	return ""
}

func ToInt(v string, def int) int {
	i, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return i
}

func ErrMissing(key string) error {
	return errors.New("missing required config key: " + key)
}