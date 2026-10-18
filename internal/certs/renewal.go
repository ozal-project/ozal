package certs

import "time"

// RenewalDue reports whether a certificate valid from notBefore to notAfter
// must be renewed at now.
//
// The boundary is the beginning of the last third of the certificate's
// actual lifetime, including the exact instant: a 90-day certificate is
// renewed when a third of its life is left, a 7-day one after two thirds of
// its life. Using a fraction of the real lifetime instead of a fixed number
// of days keeps the decision correct for short-lived and long-lived
// certificates alike.
//
// Unusable values return true on purpose: a zero validity time, an empty or
// reversed interval, or a certificate that is not valid yet means the caller
// must re-check reality instead of treating broken state as healthy. A
// certificate at or past its expiry is due as well.
//
// RenewalDue only decides that an attempt is due; it is not a schedule. The
// caller serializes attempts and applies backoff, so a true result never
// means "issue in a tight loop".
func RenewalDue(now, notBefore, notAfter time.Time) bool {
	switch {
	case notBefore.IsZero() || notAfter.IsZero():
		return true
	case !notAfter.After(notBefore):
		return true
	case now.Before(notBefore):
		return true
	}
	boundary := notAfter.Add(-notAfter.Sub(notBefore) / 3)
	return !now.Before(boundary)
}
