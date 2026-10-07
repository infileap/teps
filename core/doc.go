// Package core is the microkernel: the only four concepts that earn a place
// here.
//
// Admission is by property, not by taste. A concept belongs in this package
// only if it is (1) the single authority for something, (2) atomic, or
// (3) on every path. A concept that is merely a pure function of state that
// already exists elsewhere does not qualify, however central it feels.
//
// The four concepts are Frame, Log, Budget and Refusal. They are specified in
// docs/KERNEL.md, together with the rule that keeps this package small.
//
// This package must stay close to stateless. The maintenance argument is not
// about line count: the cost of upgrading a kernel is dominated by migrating
// the state it holds, so the way to make upgrades cheap is to hold as little
// as possible and keep the durable truth on disk. Restart is a supported
// operation, not a failure: swap the binary, replay the log.
//
// Vocabulary rule: the domain vocabulary of the layers above is banned in this
// package. If one of those words is needed here, the concept belongs in
// organs/ instead. The exact word list lives in tools/check_kernel.py
// (DOMAIN_WORDS) and is deliberately not repeated here, so this file cannot
// drift away from the checker.
//
// Nothing in this package is implemented yet. It exists so that the boundary
// is a file rather than a promise, and so that tools/check_kernel.py has
// something real to measure instead of passing vacuously.
package core
