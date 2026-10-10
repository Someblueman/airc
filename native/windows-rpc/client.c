#include "common.h"
#include "gateway.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

int main(int argc, char **argv) {
    int exit_code = 1;
    RPC_WSTR string_binding = NULL;
    RPC_BINDING_HANDLE binding = NULL;
    PSID server_sid = NULL;
    PSID client_sid = NULL;
    uint8_t request[AIRC_MAX_REQUEST];
    uint8_t response[AIRC_MAX_RESPONSE];
    uint32_t request_size = 0;
    unsigned long response_size = 0;
    RPC_STATUS status;
    RPC_SECURITY_QOS_V3_W qos;
    if (!airc_apply_job_limits()) {
        airc_diagnostic("could not establish worker job limits");
        goto cleanup;
    }
    if (!airc_start_watchdog()) {
        airc_diagnostic("could not start worker watchdog");
        goto cleanup;
    }
    if (argc != 3 || !airc_stdio_is_piped()) {
        airc_diagnostic("invalid arguments or stdio is not pipe-backed");
        goto cleanup;
    }
    if (!airc_parse_sid(argv[1], &server_sid) || !airc_parse_sid(argv[2], &client_sid) || !airc_token_matches(client_sid)) {
        airc_diagnostic("worker SID validation failed");
        goto cleanup;
    }
    if (!airc_read_frame(GetStdHandle(STD_INPUT_HANDLE), AIRC_MAX_REQUEST, request, &request_size)) {
        airc_diagnostic("could not read bounded request frame");
        goto cleanup;
    }
    status = RpcStringBindingComposeW(NULL, (RPC_WSTR)L"ncalrpc", NULL, NULL, NULL, &string_binding);
    if (status != RPC_S_OK) goto rpc_failure;
    status = RpcBindingFromStringBindingW(string_binding, &binding);
    if (status != RPC_S_OK) goto rpc_failure;
    memset(&qos, 0, sizeof(qos));
    qos.Version = RPC_C_SECURITY_QOS_VERSION_3;
    qos.Capabilities = RPC_C_QOS_CAPABILITIES_LOCAL_MA_HINT | RPC_C_QOS_CAPABILITIES_MUTUAL_AUTH;
    qos.IdentityTracking = RPC_C_QOS_IDENTITY_STATIC;
    qos.ImpersonationType = RPC_C_IMP_LEVEL_IDENTIFY;
    qos.AdditionalSecurityInfoType = 0;
    qos.Sid = server_sid;
    status = RpcBindingSetAuthInfoExW(binding, NULL, RPC_C_AUTHN_LEVEL_PKT_PRIVACY, RPC_C_AUTHN_WINNT, NULL,
                                      RPC_C_AUTHZ_NONE, (RPC_SECURITY_QOS *)&qos);
    if (status != RPC_S_OK) goto rpc_failure;
    status = RpcBindingSetOption(binding, RPC_C_OPT_CALL_TIMEOUT, 20000);
    if (status != RPC_S_OK) goto rpc_failure;
    RpcTryExcept {
        status = Exchange(binding, request_size, request, &response_size, response);
    }
    RpcExcept(1) {
        status = RpcExceptionCode();
    }
    RpcEndExcept
    if (status != RPC_S_OK) goto rpc_failure;
    if (response_size == 0 || response_size > AIRC_MAX_RESPONSE ||
        !airc_write_frame(GetStdHandle(STD_OUTPUT_HANDLE), response, (uint32_t)response_size)) {
        airc_diagnostic("could not write bounded response frame");
        goto cleanup;
    }
    exit_code = 0;
    goto cleanup;
rpc_failure:
    airc_diagnostic("authenticated RPC exchange failed");
cleanup:
    if (binding != NULL && RpcBindingFree(&binding) != RPC_S_OK) exit_code = 1;
    if (string_binding != NULL && RpcStringFreeW(&string_binding) != RPC_S_OK) exit_code = 1;
    if (server_sid != NULL) LocalFree(server_sid);
    if (client_sid != NULL) LocalFree(client_sid);
    airc_stop_watchdog();
    return exit_code;
}
