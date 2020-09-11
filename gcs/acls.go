package gcs

import "cloud.google.com/go/storage"

// WithACLEntry appends a ACL Rule to the ACL that will be set on new object
// versions written by this RaceDecider.
func WithACLEntry(rule storage.ACLRule) DeciderOpts {
	return func(cfg *deciderOptions) {
		cfg.acls = append(cfg.acls, rule)
	}
}

// WithObjectReader appends a rule granting readonly access to the ACL entity
// argument to the ACL that will be set on new object versions written by this
// RaceDecider.
func WithObjectReader(reader storage.ACLEntity) DeciderOpts {
	rule := storage.ACLRule{
		Entity: reader,
		Role:   storage.RoleReader,
	}
	return WithACLEntry(rule)
}
