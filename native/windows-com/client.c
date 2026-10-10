#include "common.h"

#include <string.h>
#include <wchar.h>

static bool initialize_client_security(void) {
    SOLE_AUTHENTICATION_SERVICE auth;
    auth.dwAuthnSvc = RPC_C_AUTHN_WINNT;
    auth.dwAuthzSvc = RPC_C_AUTHZ_NONE;
    auth.pPrincipalName = NULL;
    return SUCCEEDED(CoInitializeSecurity(NULL, 1, &auth, NULL, RPC_C_AUTHN_LEVEL_PKT_PRIVACY,
                                          RPC_C_IMP_LEVEL_IDENTIFY, NULL,
                                          EOAC_DISABLE_AAA | EOAC_NO_CUSTOM_MARSHAL, NULL));
}

int main(int argc, char **argv) {
    int exit_code = 1;
    PSID server_sid = NULL;
    PSID client_sid = NULL;
    IStream *stream = NULL;
    IDispatch *dispatch = NULL;
    VARIANT argument;
    VARIANT result;
    DISPPARAMS params;
    LPOLESTR method_name = L"Exchange";
    DISPID method_id = DISPID_UNKNOWN;
    EXCEPINFO exception;
    uint8_t object_data[AIRC_MAX_OBJREF];
    uint8_t request[AIRC_MAX_REQUEST];
    uint8_t response[AIRC_MAX_RESPONSE];
    uint32_t object_size = 0;
    uint32_t request_size = 0;
    HRESULT hr;
    bool com_initialized = false;
    UINT chars = 0;
    ULONG written = 0;
    uint32_t response_size = 0;
    LARGE_INTEGER zero;
    if (!airc_apply_job_limits() || !airc_start_watchdog()) {
        airc_diagnostic("could not establish worker limits");
        goto cleanup;
    }
    if (argc != 3 || !airc_stdio_is_piped() ||
        !airc_parse_sid(argv[1], &server_sid) || !airc_parse_sid(argv[2], &client_sid) ||
        !airc_token_matches(client_sid)) {
        airc_diagnostic("worker arguments, pipes, or identity are invalid");
        goto cleanup;
    }
    if (!airc_read_frame(GetStdHandle(STD_INPUT_HANDLE), AIRC_MAX_OBJREF, object_data, &object_size) ||
        !airc_validate_objref(object_data, object_size) ||
        !airc_read_frame(GetStdHandle(STD_INPUT_HANDLE), AIRC_MAX_REQUEST, request, &request_size)) {
        airc_diagnostic("could not read bounded COM request");
        goto cleanup;
    }
    hr = CoInitializeEx(NULL, COINIT_MULTITHREADED);
    if (FAILED(hr)) goto cleanup;
    com_initialized = true;
    if (!initialize_client_security()) goto cleanup;
    hr = CreateStreamOnHGlobal(NULL, TRUE, &stream);
    if (FAILED(hr)) goto cleanup;
    hr = IStream_Write(stream, object_data, object_size, &written);
    if (FAILED(hr) || written != object_size) goto cleanup;
    zero.QuadPart = 0;
    if (FAILED(IStream_Seek(stream, zero, STREAM_SEEK_SET, NULL))) goto cleanup;
    hr = CoUnmarshalInterface(stream, &IID_IDispatch, (void **)&dispatch);
    if (FAILED(hr) || dispatch == NULL) goto cleanup;
    hr = CoSetProxyBlanket((IUnknown *)dispatch, RPC_C_AUTHN_WINNT, RPC_C_AUTHZ_NONE, NULL,
                           RPC_C_AUTHN_LEVEL_PKT_PRIVACY, RPC_C_IMP_LEVEL_IDENTIFY, NULL, EOAC_NONE);
    if (FAILED(hr)) goto cleanup;
    hr = IDispatch_GetIDsOfNames(dispatch, &IID_NULL, &method_name, 1, LOCALE_INVARIANT, &method_id);
    if (FAILED(hr) || method_id != 1) goto cleanup;
    {
        int wide_count = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, (const char *)request,
                                              (int)request_size, NULL, 0);
        if (wide_count <= 0 || wide_count > (int)AIRC_MAX_REQUEST || memchr(request, 0, request_size) != NULL) goto cleanup;
        VariantInit(&argument);
        argument.vt = VT_BSTR;
        argument.bstrVal = SysAllocStringLen(NULL, (UINT)wide_count);
        if (argument.bstrVal == NULL || MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, (const char *)request,
                                                             (int)request_size, argument.bstrVal, wide_count) != wide_count) {
            VariantClear(&argument);
            goto cleanup;
        }
        VariantInit(&result);
        ZeroMemory(&params, sizeof(params));
        params.rgvarg = &argument;
        params.cArgs = 1;
        ZeroMemory(&exception, sizeof(exception));
        hr = IDispatch_Invoke(dispatch, method_id, &IID_NULL, LOCALE_INVARIANT, DISPATCH_METHOD,
                              &params, &result, &exception, NULL);
        VariantClear(&argument);
        if (FAILED(hr) || result.vt != VT_BSTR || result.bstrVal == NULL) {
            VariantClear(&result);
            goto cleanup;
        }
        chars = SysStringLen(result.bstrVal);
        if (chars == 0 || chars > AIRC_MAX_RESPONSE || wmemchr(result.bstrVal, L'\0', chars) != NULL) {
            VariantClear(&result);
            goto cleanup;
        }
        {
            int bytes = WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, result.bstrVal, (int)chars,
                                             (char *)response, AIRC_MAX_RESPONSE, NULL, NULL);
            if (bytes <= 0 || bytes > (int)AIRC_MAX_RESPONSE) {
                VariantClear(&result);
                goto cleanup;
            }
            response_size = (uint32_t)bytes;
        }
        VariantClear(&result);
    }
    IDispatch_Release(dispatch);
    dispatch = NULL;
    IStream_Release(stream);
    stream = NULL;
    CoUninitialize();
    com_initialized = false;
    if (!airc_write_frame(GetStdHandle(STD_OUTPUT_HANDLE), response, response_size)) goto cleanup;
    exit_code = 0;
cleanup:
    if (dispatch != NULL) IDispatch_Release(dispatch);
    if (stream != NULL) IStream_Release(stream);
    if (com_initialized) CoUninitialize();
    if (server_sid != NULL) LocalFree(server_sid);
    if (client_sid != NULL) LocalFree(client_sid);
    SecureZeroMemory(object_data, sizeof(object_data));
    SecureZeroMemory(request, sizeof(request));
    SecureZeroMemory(response, sizeof(response));
    airc_stop_watchdog();
    return exit_code;
}
