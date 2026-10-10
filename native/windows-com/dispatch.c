#include "common.h"

#include <rpcasync.h>
#include <stdlib.h>
#include <string.h>
#include <wchar.h>

typedef struct {
    IDispatch iface;
    volatile LONG references;
    PSID client_sid;
    HANDLE input;
    HANDLE output;
    HANDLE finished;
    HANDLE drained;
    CRITICAL_SECTION call_lock;
    LONG active_calls;
    LONG stopping;
    LONG invoked;
} gateway_dispatch;

static bool enter_call(gateway_dispatch *object) {
    bool accepted = false;
    EnterCriticalSection(&object->call_lock);
    if (object->stopping == 0) {
        if (object->active_calls++ == 0) ResetEvent(object->drained);
        accepted = true;
    }
    LeaveCriticalSection(&object->call_lock);
    return accepted;
}

static void leave_call(gateway_dispatch *object) {
    EnterCriticalSection(&object->call_lock);
    if (--object->active_calls == 0) SetEvent(object->drained);
    LeaveCriticalSection(&object->call_lock);
}

static gateway_dispatch *from_iface(IDispatch *iface) {
    return (gateway_dispatch *)iface;
}

static HRESULT STDMETHODCALLTYPE query_interface(IDispatch *iface, REFIID iid, void **out) {
    if (out == NULL) return E_POINTER;
    *out = NULL;
    if (iid == NULL || (!IsEqualIID(iid, &IID_IUnknown) && !IsEqualIID(iid, &IID_IDispatch))) return E_NOINTERFACE;
    *out = iface;
    (void)InterlockedIncrement(&from_iface(iface)->references);
    return S_OK;
}

static ULONG STDMETHODCALLTYPE add_ref(IDispatch *iface) {
    return (ULONG)InterlockedIncrement(&from_iface(iface)->references);
}

static ULONG STDMETHODCALLTYPE release(IDispatch *iface) {
    gateway_dispatch *object = from_iface(iface);
    LONG remaining = InterlockedDecrement(&object->references);
    if (remaining == 0) {
        if (object->client_sid != NULL) LocalFree(object->client_sid);
        if (object->finished != NULL) CloseHandle(object->finished);
        if (object->drained != NULL) CloseHandle(object->drained);
        DeleteCriticalSection(&object->call_lock);
        SecureZeroMemory(object, sizeof(*object));
        free(object);
    }
    return (ULONG)remaining;
}

static HRESULT STDMETHODCALLTYPE get_type_info_count(IDispatch *iface, UINT *count) {
    (void)iface;
    if (count == NULL) return E_POINTER;
    *count = 0;
    return S_OK;
}

static HRESULT STDMETHODCALLTYPE get_type_info(IDispatch *iface, UINT index, LCID lcid, ITypeInfo **info) {
    (void)iface;
    (void)index;
    (void)lcid;
    if (info != NULL) *info = NULL;
    return E_NOTIMPL;
}

static HRESULT STDMETHODCALLTYPE get_ids_of_names(IDispatch *iface, REFIID iid, LPOLESTR *names, UINT count,
                                                   LCID lcid, DISPID *ids) {
    (void)iface;
    (void)lcid;
    if (iid == NULL || !IsEqualIID(iid, &IID_NULL) || names == NULL || ids == NULL || count != 1 ||
        names[0] == NULL || wcscmp(names[0], L"Exchange") != 0) return DISP_E_UNKNOWNNAME;
    ids[0] = 1;
    return S_OK;
}

static bool caller_is_authorized(gateway_dispatch *object) {
    RPC_CALL_ATTRIBUTES_V2_W attributes;
    HANDLE token = NULL;
    union {
        TOKEN_USER alignment;
        BYTE bytes[sizeof(TOKEN_USER) + SECURITY_MAX_SID_SIZE];
    } storage;
    TOKEN_USER *user = (TOKEN_USER *)storage.bytes;
    DWORD needed = 0;
    bool authorized = false;
    RPC_STATUS status;
    ZeroMemory(&attributes, sizeof(attributes));
    attributes.Version = 2;
    status = RpcServerInqCallAttributesW(NULL, &attributes);
    if (status != RPC_S_OK || attributes.ProtocolSequence != RPC_PROTSEQ_LRPC ||
        attributes.IsClientLocal != rcclLocal || attributes.AuthenticationService != RPC_C_AUTHN_WINNT ||
        attributes.AuthenticationLevel != RPC_C_AUTHN_LEVEL_PKT_PRIVACY || attributes.NullSession ||
        attributes.KernelModeCaller) return false;
    if (CoImpersonateClient() != S_OK) return false;
    if (OpenThreadToken(GetCurrentThread(), TOKEN_QUERY, TRUE, &token)) {
        if (GetTokenInformation(token, TokenUser, storage.bytes, (DWORD)sizeof(storage.bytes), &needed) &&
            needed >= sizeof(TOKEN_USER) && needed <= sizeof(storage.bytes)) {
            uintptr_t sid_address = (uintptr_t)user->User.Sid;
            uintptr_t begin = (uintptr_t)storage.bytes;
            uintptr_t end = begin + needed;
            if (sid_address >= begin && sid_address <= end && end - sid_address >= 8u) {
                DWORD sid_length = GetLengthSid((PSID)sid_address);
                if (sid_length != 0 && sid_length <= end - sid_address && IsValidSid((PSID)sid_address) &&
                    EqualSid((PSID)sid_address, object->client_sid)) authorized = true;
            }
        }
        if (!CloseHandle(token)) authorized = false;
    }
    if (CoRevertToSelf() != S_OK) ExitProcess(1);
    return authorized;
}

static HRESULT STDMETHODCALLTYPE invoke(IDispatch *iface, DISPID id, REFIID iid, LCID lcid, WORD flags,
                                         DISPPARAMS *params, VARIANT *result, EXCEPINFO *exception,
                                         UINT *argument_error) {
    gateway_dispatch *object = from_iface(iface);
    BSTR input_text = NULL;
    BSTR output_text = NULL;
    uint8_t request[AIRC_MAX_REQUEST];
    uint8_t response[AIRC_MAX_RESPONSE];
    UINT chars;
    int byte_count;
    uint32_t response_size = 0;
    HRESULT hr = E_FAIL;
    bool admitted = false;
    (void)lcid;
    (void)exception;
    (void)argument_error;
    if (!enter_call(object)) return RPC_E_SERVER_TOO_BUSY;
    if (result == NULL) {
        hr = E_POINTER;
        goto done;
    }
    VariantInit(result);
    if (id != 1 || iid == NULL || !IsEqualIID(iid, &IID_NULL) || flags != DISPATCH_METHOD || params == NULL ||
        params->cArgs != 1 || params->cNamedArgs != 0 || params->rgvarg == NULL || params->rgdispidNamedArgs != NULL ||
        params->rgvarg[0].vt != VT_BSTR || params->rgvarg[0].bstrVal == NULL) {
        hr = DISP_E_BADPARAMCOUNT;
        goto done;
    }
    if (!caller_is_authorized(object)) {
        hr = E_ACCESSDENIED;
        goto done;
    }
    if (InterlockedCompareExchange(&object->invoked, 1, 0) != 0) {
        hr = RPC_E_SERVER_TOO_BUSY;
        goto done;
    }
    admitted = true;
    input_text = params->rgvarg[0].bstrVal;
    chars = SysStringLen(input_text);
    if (chars == 0 || chars > AIRC_MAX_REQUEST || wmemchr(input_text, L'\0', chars) != NULL) {
        hr = E_INVALIDARG;
        goto done;
    }
    byte_count = WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, input_text, (int)chars, NULL, 0, NULL, NULL);
    if (byte_count <= 0 || byte_count > (int)AIRC_MAX_REQUEST) {
        hr = E_INVALIDARG;
        goto done;
    }
    if (WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, input_text, (int)chars, (char *)request,
                            byte_count, NULL, NULL) != byte_count) {
        hr = E_INVALIDARG;
        goto done;
    }
    if (!airc_write_frame(object->output, request, (uint32_t)byte_count) ||
        !airc_read_frame(object->input, AIRC_MAX_RESPONSE, response, &response_size)) goto done;
    if (memchr(response, 0, response_size) != NULL) goto done;
    {
        int wide_count = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, (const char *)response,
                                              (int)response_size, NULL, 0);
        if (wide_count <= 0 || wide_count > (int)AIRC_MAX_RESPONSE) goto done;
        output_text = SysAllocStringLen(NULL, (UINT)wide_count);
        if (output_text == NULL || MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, (const char *)response,
                                                       (int)response_size, output_text, wide_count) != wide_count) goto done;
    }
    result->vt = VT_BSTR;
    result->bstrVal = output_text;
    output_text = NULL;
    hr = S_OK;
done:
    if (output_text != NULL) SysFreeString(output_text);
    SecureZeroMemory(request, sizeof(request));
    SecureZeroMemory(response, sizeof(response));
    if (admitted && object->finished != NULL) (void)SetEvent(object->finished);
    leave_call(object);
    return hr;
}

static const IDispatchVtbl dispatch_vtable = {
    query_interface,
    add_ref,
    release,
    get_type_info_count,
    get_type_info,
    get_ids_of_names,
    invoke
};

HRESULT airc_create_dispatch(PSID client_sid, HANDLE finished, IDispatch **dispatch_out) {
    gateway_dispatch *object;
    DWORD sid_size;
    DWORD error = ERROR_NOT_ENOUGH_MEMORY;
    if (client_sid == NULL || finished == NULL || dispatch_out == NULL) return E_INVALIDARG;
    *dispatch_out = NULL;
    object = (gateway_dispatch *)calloc(1, sizeof(*object));
    if (object == NULL) return E_OUTOFMEMORY;
    InitializeCriticalSection(&object->call_lock);
    object->drained = CreateEventW(NULL, TRUE, TRUE, NULL);
    sid_size = GetLengthSid(client_sid);
    object->client_sid = sid_size == 0 ? NULL : LocalAlloc(LMEM_FIXED, sid_size);
    if (object->drained == NULL || object->client_sid == NULL ||
        !CopySid(sid_size, object->client_sid, client_sid)) {
        error = GetLastError();
        if (error == ERROR_SUCCESS) error = ERROR_NOT_ENOUGH_MEMORY;
        if (object->client_sid != NULL) LocalFree(object->client_sid);
        if (object->drained != NULL) CloseHandle(object->drained);
        DeleteCriticalSection(&object->call_lock);
        free(object);
        return HRESULT_FROM_WIN32(error);
    }
    if (!DuplicateHandle(GetCurrentProcess(), finished, GetCurrentProcess(), &object->finished,
                         0, FALSE, DUPLICATE_SAME_ACCESS)) {
        error = GetLastError();
        if (object->client_sid != NULL) LocalFree(object->client_sid);
        if (object->drained != NULL) CloseHandle(object->drained);
        DeleteCriticalSection(&object->call_lock);
        free(object);
        return HRESULT_FROM_WIN32(error);
    }
    object->iface.lpVtbl = &dispatch_vtable;
    object->references = 1;
    object->input = GetStdHandle(STD_INPUT_HANDLE);
    object->output = GetStdHandle(STD_OUTPUT_HANDLE);
    *dispatch_out = &object->iface;
    return S_OK;
}

bool airc_dispatch_stop_and_wait(IDispatch *dispatch, DWORD timeout_ms) {
    gateway_dispatch *object;
    bool stopped;
    if (dispatch == NULL) return false;
    object = from_iface(dispatch);
    EnterCriticalSection(&object->call_lock);
    object->stopping = 1;
    if (object->active_calls == 0) SetEvent(object->drained);
    LeaveCriticalSection(&object->call_lock);
    stopped = WaitForSingleObject(object->drained, timeout_ms) == WAIT_OBJECT_0;
    return stopped;
}

