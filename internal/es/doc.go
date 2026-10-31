// Package es is the event-sourcing kit over JetStream shared by the citadel
// and the daemon: appending events that record the command id that produced
// them, compare-and-set appends on per-aggregate subjects, ordered consumers
// for projections, and stream and mirror creation that is safe to repeat.
package es
