package su.svn.core.controllers;

import com.fasterxml.jackson.databind.ObjectMapper;
import org.junit.jupiter.api.Test;
import org.mockito.Mockito;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.WebMvcTest;
import org.springframework.boot.test.context.TestConfiguration;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Import;
import org.springframework.http.MediaType;
import org.springframework.test.web.servlet.MockMvc;
import su.svn.core.models.dto.NewJsonRecord;
import su.svn.core.models.dto.ResourceJsonRecord;
import su.svn.core.services.domain.JsonRecordService;

import java.time.OffsetDateTime;
import java.util.Map;
import java.util.UUID;

import static org.mockito.Mockito.*;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.jsonPath;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

@Import(JsonRecordControllerTest.TestConfig.class)
@WebMvcTest(JsonRecordController.class)
class JsonRecordControllerTest {

    @Autowired
    private MockMvc mockMvc;

    @Autowired
    private JsonRecordService jsonRecordService;

    @Autowired
    private ObjectMapper objectMapper;

    // -----------------------------
    // GET by id
    // -----------------------------
    @Test
    void shouldReturnRecordById() throws Exception {
        UUID id = UUID.randomUUID();

        ResourceJsonRecord response = ResourceJsonRecord.builder()
                .id(id)
                .title("test")
                .values(Map.of("key", "value"))
                .postAt(OffsetDateTime.now())
                .visible(true)
                .flags(1)
                .build();

        when(jsonRecordService.findById(id)).thenReturn(response);

        mockMvc.perform(get("/core/api/v2/json-record/{id}", id))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.id").value(id.toString()))
                .andExpect(jsonPath("$.title").value("test"));

        verify(jsonRecordService).findById(id);
    }

    // -----------------------------
    // POST create
    // -----------------------------
    @Test
    void shouldCreateRecord() throws Exception {

        NewJsonRecord request = NewJsonRecord.builder()
                .title("new title")
                .values(Map.of("a", "b"))
                .postAt(OffsetDateTime.now())
                .visible(true)
                .flags(0)
                .build();

        ResourceJsonRecord response = ResourceJsonRecord.builder()
                .id(UUID.randomUUID())
                .title("new title")
                .values(Map.of("a", "b"))
                .visible(true)
                .flags(0)
                .build();

        when(jsonRecordService.save(any())).thenReturn(response);

        mockMvc.perform(post("/core/api/v2/json-record")
                        .contentType(MediaType.APPLICATION_JSON)
                        .content(objectMapper.writeValueAsString(request)))
                .andExpect(status().isCreated())
                .andExpect(jsonPath("$.title").value("new title"));

        verify(jsonRecordService).save(any());
    }

    // -----------------------------
    // PUT update
    // -----------------------------
    @Test
    void shouldUpdateRecord() throws Exception {

        UUID id = UUID.randomUUID();

        var request = Map.of(
                "id", id.toString(),
                "title", "updated"
        );

        ResourceJsonRecord response = ResourceJsonRecord.builder()
                .id(id)
                .title("updated")
                .build();

        when(jsonRecordService.update(any())).thenReturn(response);

        mockMvc.perform(put("/core/api/v2/json-record")
                        .contentType(MediaType.APPLICATION_JSON)
                        .content(objectMapper.writeValueAsString(request)))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.title").value("updated"));

        verify(jsonRecordService).update(any());
    }

    // -----------------------------
    // DELETE (disable)
    // -----------------------------
    @Test
    void shouldDisableRecord() throws Exception {

        UUID id = UUID.randomUUID();

        doNothing().when(jsonRecordService).disable(id);

        mockMvc.perform(delete("/core/api/v2/json-record/{id}", id))
                .andExpect(status().isNoContent());

        verify(jsonRecordService).disable(id);
    }

    @TestConfiguration
    static class TestConfig {
        @Bean
        public JsonRecordService jsonRecordService() {
            return Mockito.mock(JsonRecordService.class);
        }
    }
}