// Package naive holds the naive variants of the defenses, the code a
// reasonable developer writes in a hurry (docs/decisions/ADR-012). They are
// compiled only into chaoslab builds and registered with the defenses
// registry; the composition root picks them for the defenses the profile
// turns off.
package naive
