package config

import (
	"path/filepath"

	"github.com/ilyakaznacheev/cleanenv"
)

type Path string

func (p Path) Join(elem ...string) Path {
	parts := append([]string{string(p)}, elem...)
	return Path(filepath.Join(parts...))
}

func (p Path) ToString() string {
	return string(p)
}

// parses the configuration file first, then reads
// environment variables and overwrites the file
// values with any matching env vars it finds
func Load(path Path, cfg any) error {
	err := cleanenv.ReadConfig(path.ToString(), cfg)
	return err
}
