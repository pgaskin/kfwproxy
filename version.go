package main

import (
	"fmt"
	"regexp"
	"strconv"
)

type Version [3]uint64

var versionRe = regexp.MustCompile(`([0-9]+)\.([0-9]+)(?:\.([0-9]+))?`)

func MustExtractVersion(str string) Version {
	m := versionRe.FindStringSubmatch(str)
	var v Version
	var err error
	for i := range v {
		if i+1 < len(m) && m[i+1] != "" {
			v[i], err = strconv.ParseUint(m[i+1], 10, 64)
			if err != nil {
				panic(err)
			}
		}
	}
	return v
}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2])
}

func (v Version) Less(w Version) bool {
	return !(v[0] > w[0] || (v[0] == w[0] && (v[1] > w[1] || (v[1] == w[1] && (v[2] > w[2] || v[2] == w[2])))))
}

func (v Version) Equal(w Version) bool {
	return v[0] == w[0] && v[1] == w[1] && v[2] == w[2]
}

func (v Version) Zero() bool {
	return v[0] == 0 && v[1] == 0 && v[2] == 0
}
