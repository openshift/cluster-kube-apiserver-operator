// this package should be removed in 4.9.
// for 4.8 it is necessary to watch for oauthconfig until we are ready to use the webhook authenticator.
// after the webhook authenticator configuration is ready for use, then these settings should be cleared and never set again.
// This is because when webhook configuration is set, the webhook is ready to be used as authoritative.
package oauth
