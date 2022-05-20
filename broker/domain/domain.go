// Copyright 2022 OnMetal authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package domain

import (
	"fmt"
	"strings"
)

type Domain string

func (d Domain) Subdomain(subdomainName string) Domain {
	if subdomainName == "" {
		return d
	}
	return Domain(fmt.Sprintf("%s.%s", subdomainName, d))
}

func (d Domain) Slash(name string) string {
	return fmt.Sprintf("%s/%s", d, name)
}

func (d Domain) SubdomainSlash(subdomainName, name string) string {
	return d.Subdomain(subdomainName).Slash(name)
}

func (d Domain) String() string {
	return string(d)
}

func (d Domain) IsZero() bool {
	return d == ""
}

func (d Domain) IsSubdomain() bool {
	sub, _ := d.Split()
	return sub != ""
}

func (d Domain) Split() (subdomain string, domain Domain) {
	parts := strings.SplitN(string(d), ".", 2)
	if len(parts) == 1 {
		return "", d
	}
	return parts[0], Domain(parts[1])
}
