#ifndef NET_SWITCH_WATCHER_H
#define NET_SWITCH_WATCHER_H

#include <stdint.h>

typedef struct NetSwitchWatcher NetSwitchWatcher;

NetSwitchWatcher *NetSwitchWatcherCreate(uintptr_t handle);
void NetSwitchWatcherStop(NetSwitchWatcher *watcher);
extern void netSwitchNetworkChanged(uintptr_t handle);

#endif
