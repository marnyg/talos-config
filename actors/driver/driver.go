// Package driver holds what every platform driver and the child agree
// on: how the intro reaches the container.
package driver

// ParamsEnv is the env var the child reads its intro (the #spawn
// params, verbatim) from. Every driver sets it; cmd/child reads it.
const ParamsEnv = "SAP_INTRO"
