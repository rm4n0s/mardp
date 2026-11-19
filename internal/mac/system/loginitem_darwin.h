// The C side of LoginItem; loginitem_darwin.m implements it.
#pragma once

// mardpLoginItemStatus is SMAppService.mainAppService.status.
int mardpLoginItemStatus(void);

// mardpLoginResult is what mardpSetLoginItem returns: code 0, or the
// NSError's code and its description, which the caller frees.
typedef struct {
	long code;
	char *message;
} mardpLoginResult;

// mardpSetLoginItem registers the main app service, or unregisters it.
mardpLoginResult mardpSetLoginItem(int on);
