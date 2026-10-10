#include "common.h"

#include <rpcasync.h>
#include <sddl.h>
#include <stdlib.h>
#include <string.h>

#define AIRC_SERVER_TIMEOUT_MS 30000u

static bool build_security(PSID server_sid, PSID client_sid, SECURITY_DESCRIPTOR *sd,
                           airc_acl_storage *acl_storage) {
    ACL *acl = (ACL *)acl_storage->bytes;
    DWORD access = COM_RIGHTS_EXECUTE | COM_RIGHTS_EXECUTE_LOCAL;
    if (!InitializeAcl(acl, (DWORD)sizeof(acl_storage->bytes), ACL_REVISION) ||
        !AddAccessAllowedAce(acl, ACL_REVISION, access, server_sid) ||
        !AddAccessAllowedAce(acl, ACL_REVISION, access, client_sid) ||
        !InitializeSecurityDescriptor(sd, SECURITY_DESCRIPTOR_REVISION) ||
        !SetSecurityDescriptorOwner(sd, server_sid, FALSE) ||
        !SetSecurityDescriptorGroup(sd, server_sid, FALSE) ||
        !SetSecurityDescriptorDacl(sd, TRUE, acl, FALSE) ||
        !SetSecurityDescriptorControl(sd, SE_DACL_PROTECTED, SE_DACL_PROTECTED)) return false;
    return IsValidSecurityDescriptor(sd) != FALSE;
}

static bool initialize_com_security(PSID server_sid, PSID client_sid,
                                    SECURITY_DESCRIPTOR *sd,
                                    airc_acl_storage *acl_storage) {
    SOLE_AUTHENTICATION_SERVICE auth;
    if (!build_security(server_sid, client_sid, sd, acl_storage)) return false;
    auth.dwAuthnSvc = RPC_C_AUTHN_WINNT;
    auth.dwAuthzSvc = RPC_C_AUTHZ_NONE;
    auth.pPrincipalName = NULL;
    return SUCCEEDED(CoInitializeSecurity(sd, 1, &auth, NULL, RPC_C_AUTHN_LEVEL_PKT_PRIVACY,
                                          RPC_C_IMP_LEVEL_IDENTIFY, NULL,
                                          EOAC_DISABLE_AAA | EOAC_NO_CUSTOM_MARSHAL, NULL));
}

static void terminate_worker(void) {
    (void)TerminateProcess(GetCurrentProcess(), 1);
    ExitProcess(1);
}

int main(int argc, char **argv) {
    int exit_code = 1;
    PSID server_sid = NULL;
    PSID client_sid = NULL;
    IDispatch *dispatch = NULL;
    IStream *stream = NULL;
    HANDLE finished = NULL;
    bool com_initialized = false;
    bool marshaled = false;
    uint8_t object_data[AIRC_MAX_OBJREF];
    uint32_t object_size = 0;
    uint32_t ack_size = 0;
    uint8_t ack[1];
    DWORD wait_result;
    HRESULT hr;
    bool published = false;
    SECURITY_DESCRIPTOR sd;
    airc_acl_storage acl_storage;
    LARGE_INTEGER zero;
    ULARGE_INTEGER position;
    if (!airc_apply_job_limits() || !airc_start_watchdog()) {
        airc_diagnostic("could not establish worker limits");
        goto cleanup;
    }
    if (argc != 3 || !airc_stdio_is_piped() ||
        !airc_parse_sid(argv[1], &server_sid) || !airc_parse_sid(argv[2], &client_sid) ||
        !airc_token_matches(server_sid)) {
        airc_diagnostic("worker arguments, pipes, or identity are invalid");
        goto cleanup;
    }
    hr = CoInitializeEx(NULL, COINIT_MULTITHREADED);
    if (FAILED(hr)) goto cleanup;
    com_initialized = true;
    if (!initialize_com_security(server_sid, client_sid, &sd, &acl_storage)) {
        airc_diagnostic("could not initialize restricted COM security");
        goto cleanup;
    }
    finished = CreateEventW(NULL, TRUE, FALSE, NULL);
    if (finished == NULL || FAILED(airc_create_dispatch(client_sid, finished, &dispatch))) goto cleanup;
    hr = CreateStreamOnHGlobal(NULL, TRUE, &stream);
    if (FAILED(hr)) goto cleanup;
    hr = CoMarshalInterface(stream, &IID_IDispatch, (IUnknown *)dispatch, MSHCTX_LOCAL, NULL, MSHLFLAGS_TABLESTRONG);
    if (FAILED(hr)) goto cleanup;
    marshaled = true;
    zero.QuadPart = 0;
    if (FAILED(IStream_Seek(stream, zero, STREAM_SEEK_CUR, &position)) || position.QuadPart < AIRC_OBJREF_STD_MIN ||
        position.QuadPart > AIRC_MAX_OBJREF || FAILED(IStream_Seek(stream, zero, STREAM_SEEK_SET, NULL))) goto cleanup;
    object_size = (uint32_t)position.QuadPart;
    {
        ULONG read = 0;
        if (FAILED(IStream_Read(stream, object_data, object_size, &read)) || read != object_size ||
            !airc_validate_objref(object_data, object_size) ||
            !airc_write_frame(GetStdHandle(STD_OUTPUT_HANDLE), object_data, object_size)) goto cleanup;
        published = true;
    }
    wait_result = WaitForSingleObject(finished, AIRC_SERVER_TIMEOUT_MS);
    if (wait_result != WAIT_OBJECT_0) {
        airc_diagnostic("COM invocation did not finish before its deadline");
        terminate_worker();
    }
    if (!airc_read_frame(GetStdHandle(STD_INPUT_HANDLE), 1, ack, &ack_size) ||
        ack_size != 1 || ack[0] != AIRC_SERVER_RELEASE_ACK) {
        airc_diagnostic("COM client completion was not confirmed");
        terminate_worker();
    }
    if (!airc_dispatch_stop_and_wait(dispatch, AIRC_SERVER_TIMEOUT_MS)) {
        airc_diagnostic("COM calls did not drain before shutdown");
        terminate_worker();
    }
    exit_code = 0;
cleanup:
    if (published && exit_code != 0) terminate_worker();
    if (dispatch != NULL) {
        HRESULT disconnect = CoDisconnectObject((IUnknown *)dispatch, 0);
        if (FAILED(disconnect)) {
            if (published) terminate_worker();
            exit_code = 1;
        }
    }
    if (marshaled && stream != NULL) {
        zero.QuadPart = 0;
        if (FAILED(IStream_Seek(stream, zero, STREAM_SEEK_SET, NULL)) || FAILED(CoReleaseMarshalData(stream))) {
            if (published) terminate_worker();
            exit_code = 1;
        }
    }
    if (stream != NULL) IStream_Release(stream);
    if (dispatch != NULL) IDispatch_Release(dispatch);
    if (finished != NULL) CloseHandle(finished);
    if (com_initialized) CoUninitialize();
    if (server_sid != NULL) LocalFree(server_sid);
    if (client_sid != NULL) LocalFree(client_sid);
    SecureZeroMemory(object_data, sizeof(object_data));
    SecureZeroMemory(ack, sizeof(ack));
    airc_stop_watchdog();
    return exit_code;
}
