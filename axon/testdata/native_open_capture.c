// Test-only FFI capture library. Does not implement TLS or Invocation.
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
char *axon_dendrite_client_open_json(const char *payload) {
 const char *path = getenv("AXON_TEST_OPEN_CAPTURE");
 FILE *f = path ? fopen(path, "a") : NULL;
 if (!f) abort();
 fprintf(f, "%s\n", payload);
 fclose(f);
 if (getenv("AXON_TEST_OPEN_REJECT"))
  return strdup("{\"ok\":false,\"error\":{\"code\":\"ENDPOINT_INVALID\",\"message\":\"TLS trust rejected\",\"source\":\"bridge\"}}");
 return strdup("{\"ok\":true,\"handle\":7}");
}
char *axon_dendrite_client_close_json(uint64_t handle) {
 (void)handle;
 return strdup("{\"ok\":true,\"removed\":true}");
}
void axon_dendrite_string_free(char *ptr) { free(ptr); }
void axon_dendrite_unary_call_json(void) { abort(); }
void axon_dendrite_server_stream_call_json(void) { abort(); }
void axon_dendrite_client_stream_call_json(void) { abort(); }
void axon_dendrite_bidi_stream_call_json(void) { abort(); }
char *axon_dendrite_descriptor_bound_invoke_json(uint64_t handle, const char *payload) {
 (void)handle;
 const char *path = getenv("AXON_TEST_INVOKE_CAPTURE");
 const char *response = getenv("AXON_TEST_INVOKE_RESPONSE");
 FILE *f = path ? fopen(path, "a") : NULL;
 if (!f || !response) abort();
 fprintf(f, "%s\n", payload);
 fclose(f);
 return strdup(response);
}
void axon_dendrite_protocol_catalog_json(void) { abort(); }
void axon_dendrite_invoke_protocol_json(void) { abort(); }
void axon_dendrite_protocol_coverage_json(void) { abort(); }
void axon_dendrite_server_stream_open_json(void) { abort(); }
void axon_dendrite_stream_next_json(void) { abort(); }
char *axon_dendrite_stream_close_json(uint64_t handle) {
 return axon_dendrite_descriptor_bound_invoke_json(handle, "raw_close");
}
void axon_dendrite_bidi_stream_open_json(void) { abort(); }
void axon_dendrite_bidi_stream_send_json(void) { abort(); }
char *axon_dendrite_descriptor_bound_stream_open_json(uint64_t handle, const char *payload) {
 return axon_dendrite_descriptor_bound_invoke_json(handle, payload);
}
char *axon_dendrite_descriptor_bound_bidi_open_json(uint64_t handle, const char *payload) {
 return axon_dendrite_descriptor_bound_invoke_json(handle,payload);
}
void axon_dendrite_descriptor_bound_bidi_send_json(void) { abort(); }
char *axon_dendrite_descriptor_bound_bidi_recv_json(uint64_t handle, const char *payload) {
 return axon_dendrite_descriptor_bound_invoke_json(handle, payload);
}
void axon_dendrite_descriptor_bound_bidi_close_json(void) { abort(); }
