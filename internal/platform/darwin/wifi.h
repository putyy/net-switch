#ifndef NET_SWITCH_WIFI_H
#define NET_SWITCH_WIFI_H

#include <stddef.h>
#include <stdint.h>

typedef struct NetSwitchLocationObserver NetSwitchLocationObserver;

enum {
    NET_SWITCH_SSID_AVAILABLE = 0,
    NET_SWITCH_SSID_PENDING = 1,
    NET_SWITCH_SSID_DENIED = 2,
    NET_SWITCH_SSID_RESTRICTED = 3,
    NET_SWITCH_SSID_UNAVAILABLE = 4
};

NetSwitchLocationObserver *NetSwitchLocationObserverCreate(uintptr_t handle);
void NetSwitchLocationObserverRequestAuthorization(NetSwitchLocationObserver *observer);
void NetSwitchLocationObserverStop(NetSwitchLocationObserver *observer);
int NetSwitchCopyCurrentSSID(const char *interface_name, char *buffer, size_t buffer_size);
extern void netSwitchLocationAuthorizationChanged(uintptr_t handle);

#endif
