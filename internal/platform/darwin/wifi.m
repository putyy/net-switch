//go:build darwin && cgo

#include "wifi.h"

#import <CoreLocation/CoreLocation.h>
#import <CoreWLAN/CoreWLAN.h>
#import <Cocoa/Cocoa.h>
#import <Foundation/Foundation.h>
#include <dispatch/dispatch.h>
#include <pthread.h>
#include <stdlib.h>
#include <string.h>

struct NetSwitchLocationObserver {
    uintptr_t handle;
    CLLocationManager *manager;
    id delegate;
};

@interface NetSwitchLocationDelegate : NSObject <CLLocationManagerDelegate>
@property(nonatomic, assign) NetSwitchLocationObserver *observer;
@end

#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
static CLAuthorizationStatus NetSwitchLocationAuthorizationStatus(void) {
    return [CLLocationManager authorizationStatus];
}
#pragma clang diagnostic pop

@implementation NetSwitchLocationDelegate
- (void)locationManagerDidChangeAuthorization:(CLLocationManager *)manager {
    (void)manager;
    NetSwitchLocationObserver *observer = self.observer;
    if (observer != NULL) {
        netSwitchLocationAuthorizationChanged(observer->handle);
    }
}
@end

static void NetSwitchLocationObserverStart(void *context) {
    NetSwitchLocationObserver *observer = context;
    if (observer == NULL || observer->manager != nil) {
        return;
    }
    @autoreleasepool {
        NetSwitchLocationDelegate *delegate = [[NetSwitchLocationDelegate alloc] init];
        CLLocationManager *manager = [[CLLocationManager alloc] init];
        delegate.observer = observer;
        manager.delegate = delegate;
        observer->delegate = delegate;
        observer->manager = manager;
    }
}

static void NetSwitchLocationObserverRequest(void *context) {
    NetSwitchLocationObserver *observer = context;
    if (observer == NULL || observer->manager == nil) {
        return;
    }
    @autoreleasepool {
        if (observer->manager.authorizationStatus == kCLAuthorizationStatusNotDetermined) {
            [[NSApplication sharedApplication] activateIgnoringOtherApps:YES];
            [observer->manager requestWhenInUseAuthorization];
        }
    }
}

static void NetSwitchLocationObserverRelease(void *context) {
    NetSwitchLocationObserver *observer = context;
    if (observer == NULL) {
        return;
    }
    @autoreleasepool {
        NetSwitchLocationDelegate *delegate = observer->delegate;
        delegate.observer = NULL;
        observer->manager.delegate = nil;
        [observer->manager release];
        [delegate release];
        observer->manager = nil;
        observer->delegate = nil;
    }
}

NetSwitchLocationObserver *NetSwitchLocationObserverCreate(uintptr_t handle) {
    NetSwitchLocationObserver *observer = calloc(1, sizeof(NetSwitchLocationObserver));
    if (observer == NULL) {
        return NULL;
    }
    observer->handle = handle;
    if (pthread_main_np() != 0) {
        NetSwitchLocationObserverStart(observer);
    } else {
        dispatch_async_f(dispatch_get_main_queue(), observer, NetSwitchLocationObserverStart);
    }
    return observer;
}

void NetSwitchLocationObserverRequestAuthorization(NetSwitchLocationObserver *observer) {
    if (observer == NULL) {
        return;
    }
    if (pthread_main_np() != 0) {
        NetSwitchLocationObserverRequest(observer);
    } else {
        dispatch_async_f(dispatch_get_main_queue(), observer, NetSwitchLocationObserverRequest);
    }
}

void NetSwitchLocationObserverStop(NetSwitchLocationObserver *observer) {
    if (observer == NULL) {
        return;
    }
    if (pthread_main_np() != 0) {
        NetSwitchLocationObserverRelease(observer);
    } else {
        dispatch_sync_f(dispatch_get_main_queue(), observer, NetSwitchLocationObserverRelease);
    }
    free(observer);
}

int NetSwitchCopyCurrentSSID(const char *interface_name, char *buffer, size_t buffer_size) {
    CLAuthorizationStatus authorization = NetSwitchLocationAuthorizationStatus();
    if (authorization == kCLAuthorizationStatusNotDetermined) {
        return NET_SWITCH_SSID_PENDING;
    }
    if (authorization == kCLAuthorizationStatusDenied) {
        return NET_SWITCH_SSID_DENIED;
    }
    if (authorization == kCLAuthorizationStatusRestricted) {
        return NET_SWITCH_SSID_RESTRICTED;
    }
    if (buffer == NULL || buffer_size == 0) {
        return NET_SWITCH_SSID_UNAVAILABLE;
    }

    @autoreleasepool {
        CWWiFiClient *client = [CWWiFiClient sharedWiFiClient];
        CWInterface *interface = nil;
        if (interface_name != NULL && interface_name[0] != '\0') {
            NSString *name = [NSString stringWithUTF8String:interface_name];
            interface = [client interfaceWithName:name];
        }
        if (interface == nil) {
            interface = [client interface];
        }
        NSString *ssid = [interface ssid];
        if (ssid == nil || [ssid lengthOfBytesUsingEncoding:NSUTF8StringEncoding] == 0) {
            return NET_SWITCH_SSID_UNAVAILABLE;
        }
        const char *value = [ssid UTF8String];
        if (value == NULL || strlen(value) >= buffer_size) {
            return NET_SWITCH_SSID_UNAVAILABLE;
        }
        strlcpy(buffer, value, buffer_size);
        return NET_SWITCH_SSID_AVAILABLE;
    }
}
