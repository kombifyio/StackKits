// Package datasubject defines the shared wire contract for account export and
// erasure requests between the Cloud coordinator and every service that stores
// data about a subject.
//
// # Authority
//
// Cloud owns the account, the subject reference and the lifecycle request and
// is the only coordinator. Each participant owns, exports and erases the data it
// holds. This package owns only the stable JSON shape, local validation and the
// completion rule; it performs no network I/O and persists nothing.
//
// # Subject reference
//
// SubjectRef is an opaque identifier resolved by Cloud. A raw e-mail address is
// never a valid subject.
//
// # Receipts
//
// A participant answers with a Receipt signed with its own Ed25519 key over the
// canonical JSON of the receipt without its signature. Backups and logs that age
// out later are reported as retained with a retained_until instant, never
// claimed as erased. The aggregate receipt carries no subject, tenant, user or
// job identifier.
package datasubject
