package store

import (
	"sort"
	"strings"

	"teps/core"
)

// Class is an SPDX license's obligation class, ordered least to most
// restrictive. The ordering IS the mechanism: a closure's class is the maximum
// over its members, so one GPL file in a permissive pile makes the pile GPL.
type Class int

const (
	Permissive Class = iota
	WeakCopyleft
	StrongCopyleft
	NetworkCopyleft
	Restricted
)

func (c Class) String() string {
	switch c {
	case Permissive:
		return "permissive"
	case WeakCopyleft:
		return "weak-copyleft"
	case StrongCopyleft:
		return "strong-copyleft"
	case NetworkCopyleft:
		return "network-copyleft"
	case Restricted:
		return "restricted"
	}
	return "unknown"
}

// licenseClass is the closed set we recognise.
//
// Deliberately small, and deliberately **closed**: an identifier that is not in
// this map is refused, never assumed permissive. "Unknown" and "harmless" are
// not the same claim, and treating them as the same is how a license obligation
// gets discovered after shipping.
var licenseClass = map[string]Class{
	// permissive
	"MIT": Permissive, "MIT-0": Permissive, "ISC": Permissive, "0BSD": Permissive,
	"BSD-2-Clause": Permissive, "BSD-3-Clause": Permissive,
	"Apache-2.0": Permissive, "Zlib": Permissive,
	"CC0-1.0": Permissive, "Unlicense": Permissive,

	// weak copyleft: obligations attach to the licensed files
	"MPL-2.0": WeakCopyleft, "EPL-2.0": WeakCopyleft,
	"LGPL-2.1-only": WeakCopyleft, "LGPL-2.1-or-later": WeakCopyleft,
	"LGPL-3.0-only": WeakCopyleft, "LGPL-3.0-or-later": WeakCopyleft,

	// strong copyleft: obligations attach to the derived work
	"GPL-2.0-only": StrongCopyleft, "GPL-2.0-or-later": StrongCopyleft,
	"GPL-3.0-only": StrongCopyleft, "GPL-3.0-or-later": StrongCopyleft,

	// network copyleft: obligations reach users over a network
	"AGPL-3.0-only": NetworkCopyleft, "AGPL-3.0-or-later": NetworkCopyleft,

	// source-available / non-free
	"SSPL-1.0": Restricted, "BUSL-1.1": Restricted,
	"Elastic-2.0": Restricted, "Proprietary": Restricted,
}

var classObligations = map[Class][]string{
	Permissive: {"retain the copyright notice", "retain the license text"},
	WeakCopyleft: {
		"retain the copyright notice", "retain the license text",
		"publish changes to the licensed files",
	},
	StrongCopyleft: {
		"retain the copyright notice", "retain the license text",
		"publish changes to the licensed files",
		"license the whole derived work under the same terms",
		"offer complete corresponding source",
	},
	NetworkCopyleft: {
		"retain the copyright notice", "retain the license text",
		"publish changes to the licensed files",
		"license the whole derived work under the same terms",
		"offer complete corresponding source",
		"offer source to users who interact over a network",
	},
	Restricted: {
		"retain the copyright notice", "retain the license text",
		"commercial use may be prohibited",
		"redistribution may be prohibited",
	},
}

// Member is one license present in a closure.
type Member struct {
	License string `json:"license"`
	Class   Class  `json:"class"`
}

// Closure is the license closure of a skill and everything it depends on.
type Closure struct {
	Class       Class    `json:"class"`
	Members     []Member `json:"members"`
	Obligations []string `json:"obligations"`
}

// UnknownLicenseRefusal is the single refusal for an unrecognised identifier.
//
// **Two gates reach this condition** — manifest validation and closure solving —
// and they must agree on the code. Two gates assigning different codes to the
// same fact is how a client ends up with a retry loop that works on one path
// and not the other, and it is the same class of drift that tools/
// check_contract.py was written to catch between the doc and the kernel.
// Here the drift was between two functions in one package.
func UnknownLicenseRefusal(id string) *core.Refusal {
	return &core.Refusal{
		Code:   core.CodeLicenseConflict,
		Reason: "unrecognised license (fail closed): " + id,
	}
}

// SolveLicense computes the closure of a root license plus the licenses of its
// dependencies.
//
// A pure function: no I/O, no dependency resolution — just the algebra.
// Resolution happens in the registration pipeline, which reads the CAS; the
// algebra is testable without a filesystem, and that is why it is separate.
//
// Fails closed. An unrecognised identifier refuses the whole closure, because
// the alternative is a policy that says "we did not recognise this, so it is
// probably fine".
func SolveLicense(root string, deps []string) (*Closure, *core.Refusal) {
	seen := map[string]Class{}
	for _, l := range append([]string{root}, deps...) {
		if l == "" {
			return nil, &core.Refusal{Code: core.CodeBadRequest, Reason: "empty license identifier"}
		}
		c, ok := licenseClass[l]
		if !ok {
			return nil, UnknownLicenseRefusal(l)
		}
		seen[l] = c
	}
	out := &Closure{Class: Permissive}
	names := make([]string, 0, len(seen))
	for l := range seen {
		names = append(names, l)
	}
	sort.Strings(names)
	for _, l := range names {
		c := seen[l]
		out.Members = append(out.Members, Member{License: l, Class: c})
		if c > out.Class {
			out.Class = c
		}
	}
	out.Obligations = append([]string(nil), classObligations[out.Class]...)
	return out, nil
}

// Policy is what the project is willing to ship.
type Policy struct {
	AllowUpTo Class `json:"allow_up_to"`
}

// DefaultPolicy permits weak copyleft and refuses strong copyleft, network
// copyleft and source-available licenses.
//
// This matches the rule the project started from: GPL/AGPL must not enter a
// closed closure. Widening it is one line, and that is the point — the decision
// shows up in a diff instead of being buried in whoever happened to run the
// install.
func DefaultPolicy() Policy { return Policy{AllowUpTo: WeakCopyleft} }

// CheckPolicy refuses a closure that exceeds what may be shipped.
func (c *Closure) CheckPolicy(p Policy) *core.Refusal {
	if c.Class <= p.AllowUpTo {
		return nil
	}
	var worst []string
	for _, m := range c.Members {
		if m.Class == c.Class {
			worst = append(worst, m.License)
		}
	}
	return &core.Refusal{
		Code: core.CodeLicenseConflict,
		Reason: "closure is " + c.Class.String() + " (from " + strings.Join(worst, ", ") +
			") but the policy allows up to " + p.AllowUpTo.String(),
	}
}

// ObligationSummary renders the obligations as one line, for logs and reports.
func (c *Closure) ObligationSummary() string {
	return strings.Join(c.Obligations, "; ")
}
