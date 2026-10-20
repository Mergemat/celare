package system

/*
#cgo LDFLAGS: -framework CoreFoundation -framework SystemConfiguration
#include <CoreFoundation/CoreFoundation.h>
#include <SystemConfiguration/SystemConfiguration.h>

// The session owns this value. configd removes it if the daemon crashes,
// even before launchd restarts it. No saved Wi-Fi/Ethernet settings change.
static SCDynamicStoreRef dns_store;

static int celare_dns(int enabled) {
	CFStringRef key = CFSTR("State:/Network/Service/sh.celare.daemon/DNS");
	if (dns_store == NULL) {
		dns_store = SCDynamicStoreCreate(NULL, CFSTR("celare"), NULL, NULL);
		if (dns_store == NULL) return SCError();
	}
	if (!enabled) {
		Boolean ok = SCDynamicStoreRemoveValue(dns_store, key);
		int err = ok ? kSCStatusOK : SCError();
		CFRelease(dns_store);
		dns_store = NULL;
		return err == kSCStatusNoKey ? kSCStatusOK : err;
	}
	CFStringRef server = CFSTR("169.254.10.1");
	CFStringRef domain = CFSTR("");
	int order = 1;
	CFNumberRef search_order = CFNumberCreate(NULL, kCFNumberIntType, &order);
	CFArrayRef servers = CFArrayCreate(NULL, (const void **)&server, 1, &kCFTypeArrayCallBacks);
	CFArrayRef domains = CFArrayCreate(NULL, (const void **)&domain, 1, &kCFTypeArrayCallBacks);
	const void *keys[] = {kSCPropNetDNSServerAddresses, kSCPropNetDNSSupplementalMatchDomains, kSCPropNetDNSSearchOrder};
	const void *values[] = {servers, domains, search_order};
	CFDictionaryRef dns = CFDictionaryCreate(NULL, keys, values, 3, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	Boolean ok = SCDynamicStoreAddTemporaryValue(dns_store, key, dns);
	int err = ok ? kSCStatusOK : SCError();
	CFRelease(dns);
	CFRelease(domains);
	CFRelease(servers);
	CFRelease(search_order);
	return err;
}
*/
import "C"

import (
	"fmt"
	"sync"
)

var dnsMu sync.Mutex

// SetTunnelDNS installs or removes a session-owned default resolver.
// Removing it reveals the system's current DNS, including changes made while
// connected. An empty match domain applies to every ordinary DNS query.
func SetTunnelDNS(enabled bool) error {
	dnsMu.Lock()
	defer dnsMu.Unlock()
	var on C.int
	if enabled {
		on = 1
	}
	if code := C.celare_dns(on); code != C.kSCStatusOK {
		return fmt.Errorf("set tunnel DNS: %s", C.GoString(C.SCErrorString(code)))
	}
	return nil
}
