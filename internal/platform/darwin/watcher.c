//go:build darwin && cgo

#include "watcher.h"

#include <CoreFoundation/CoreFoundation.h>
#include <SystemConfiguration/SystemConfiguration.h>
#include <dispatch/dispatch.h>
#include <stdlib.h>

struct NetSwitchWatcher {
    SCDynamicStoreRef store;
    dispatch_queue_t queue;
};

static void NetSwitchWatcherCallback(
    SCDynamicStoreRef store,
    CFArrayRef changed_keys,
    void *info
) {
    (void)store;
    (void)changed_keys;
    netSwitchNetworkChanged((uintptr_t)info);
}

static void NetSwitchWatcherDrain(void *context) {
    (void)context;
}

NetSwitchWatcher *NetSwitchWatcherCreate(uintptr_t handle) {
    NetSwitchWatcher *watcher = calloc(1, sizeof(NetSwitchWatcher));
    if (watcher == NULL) {
        return NULL;
    }

    SCDynamicStoreContext context = {0, (void *)handle, NULL, NULL, NULL};
    watcher->store = SCDynamicStoreCreate(
        NULL,
        CFSTR("Net Switch"),
        NetSwitchWatcherCallback,
        &context
    );
    if (watcher->store == NULL) {
        free(watcher);
        return NULL;
    }

    CFStringRef pattern = CFSTR("^State:/Network/.*");
    CFArrayRef patterns = CFArrayCreate(
        NULL,
        (const void **)&pattern,
        1,
        &kCFTypeArrayCallBacks
    );
    if (patterns == NULL || !SCDynamicStoreSetNotificationKeys(watcher->store, NULL, patterns)) {
        if (patterns != NULL) {
            CFRelease(patterns);
        }
        CFRelease(watcher->store);
        free(watcher);
        return NULL;
    }
    CFRelease(patterns);

    watcher->queue = dispatch_queue_create(
        "com.putyy.net-switch.network-watcher",
        DISPATCH_QUEUE_SERIAL
    );
    if (watcher->queue == NULL || !SCDynamicStoreSetDispatchQueue(watcher->store, watcher->queue)) {
        if (watcher->queue != NULL) {
            dispatch_release(watcher->queue);
        }
        CFRelease(watcher->store);
        free(watcher);
        return NULL;
    }
    return watcher;
}

void NetSwitchWatcherStop(NetSwitchWatcher *watcher) {
    if (watcher == NULL) {
        return;
    }
    if (watcher->store != NULL && watcher->queue != NULL) {
        SCDynamicStoreSetDispatchQueue(watcher->store, NULL);
        dispatch_sync_f(watcher->queue, NULL, NetSwitchWatcherDrain);
    }
    if (watcher->store != NULL) {
        CFRelease(watcher->store);
    }
    if (watcher->queue != NULL) {
        dispatch_release(watcher->queue);
    }
    free(watcher);
}
