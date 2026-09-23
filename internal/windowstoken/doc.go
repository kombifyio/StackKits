// Package windowstoken exposes the two process-token principals Windows uses
// for local custody: TokenUser receives the private DACL grant, while
// TokenOwner is stamped as the owner of newly created objects. The package
// has content only on Windows; this file keeps it a valid, empty package on
// other platforms so affected-package gates can load it.
package windowstoken
