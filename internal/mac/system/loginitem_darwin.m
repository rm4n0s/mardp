#import <Foundation/Foundation.h>
#import <ServiceManagement/ServiceManagement.h>
#include <stdlib.h>
#include <string.h>
#include "loginitem_darwin.h"

int mardpLoginItemStatus(void) {
	return (int)SMAppService.mainAppService.status;
}

mardpLoginResult mardpSetLoginItem(int on) {
	mardpLoginResult r = {0, NULL};
	NSError *err = nil;
	BOOL ok = on ? [SMAppService.mainAppService registerAndReturnError:&err]
	             : [SMAppService.mainAppService unregisterAndReturnError:&err];
	if (ok) {
		return r;
	}
	r.message = strdup(err ? err.localizedDescription.UTF8String : "unknown error");
	r.code = err ? (long)err.code : -1;
	if (r.code == 0) {
		r.code = -1;
	}
	return r;
}
