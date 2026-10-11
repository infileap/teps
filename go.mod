module teps

go 1.23

// Hard discipline (docs/KERNEL.md, Gate 2):
// this module has ZERO direct dependencies. The whole standard library is
// enough for framing, canonical JSON, SHA-256, Ed25519, HTTP and process
// control. A kernel that pulls a dependency tree is arguing against the
// supply-chain discipline the project claims to have.
//
// There is deliberately no require block. tools/check_kernel.py fails the
// build if one appears.
