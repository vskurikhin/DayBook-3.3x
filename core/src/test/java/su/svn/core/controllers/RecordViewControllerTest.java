package su.svn.core.controllers;

import org.jetbrains.annotations.NotNull;
import org.junit.jupiter.api.Test;
import org.mockito.Mockito;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.WebMvcTest;
import org.springframework.boot.test.context.TestConfiguration;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Import;
import org.springframework.data.domain.Page;
import org.springframework.data.domain.Pageable;
import org.springframework.data.domain.Sort;
import org.springframework.test.web.servlet.MockMvc;
import org.springframework.test.web.servlet.MvcResult;
import org.springframework.test.web.servlet.ResultHandler;
import su.svn.core.models.dto.ResourceRecordView;
import su.svn.core.models.dto.ResourceRecordViewFilter;
import su.svn.core.services.domain.RecordViewService;

import java.time.OffsetDateTime;
import java.util.Iterator;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.function.Function;

import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.jsonPath;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

@Import(RecordViewControllerTest.TestConfig.class)
@WebMvcTest(RecordViewController.class)
class RecordViewControllerTest {
    @Autowired
    private MockMvc mockMvc;

    @Autowired
    private RecordViewService recordViewService;

    // -----------------------------
    // GET
    // -----------------------------
    @Test
    void shouldReturnRecordById() throws Exception {
        UUID id = UUID.randomUUID();
        ResourceRecordViewFilter filter = new ResourceRecordViewFilter(null, null, null);
        Pageable pageable = Pageable.ofSize(20);

        ResourceRecordView response = ResourceRecordView.builder()
                .id(id)
                .title("test")
                .values(Map.of("key", "value"))
                .postAt(OffsetDateTime.now())
                .visible(true)
                .flags(1)
                .build();
        Page<ResourceRecordView> page = getResourceRecordViews(response);

        when(recordViewService.getFilteredRecords(filter, pageable)).thenReturn(page);

        mockMvc.perform(get("/core/api/v2/json-records"))
                .andExpect(status().isOk())
                .andDo(new ResultHandler() {
                    @Override
                    public void handle(MvcResult result) throws Exception {
                        System.out.println("result = " + result.getResponse().getContentAsString());
                    }
                })
                .andExpect(jsonPath("$.content").isArray())
                .andExpect(jsonPath("$.content[0].id").value(id.toString()))
                .andExpect(jsonPath("$.content[0].title").value("test"));

        verify(recordViewService).getFilteredRecords(filter, pageable);
    }

    private static @NotNull Page<ResourceRecordView> getResourceRecordViews(ResourceRecordView response) {
        Page<ResourceRecordView> page = new Page<ResourceRecordView>() {
            @Override
            public int getTotalPages() {
                return 1;
            }

            @Override
            public long getTotalElements() {
                return 1;
            }

            @Override
            public <U> Page<U> map(Function<? super ResourceRecordView, ? extends U> converter) {
                return null;
            }

            @Override
            public int getNumber() {
                return 0;
            }

            @Override
            public int getSize() {
                return 1;
            }

            @Override
            public int getNumberOfElements() {
                return 1;
            }

            @Override
            public List<ResourceRecordView> getContent() {
                return List.of(response);
            }

            @Override
            public boolean hasContent() {
                return false;
            }

            @Override
            public Sort getSort() {
                return Sort.unsorted();
            }

            @Override
            public boolean isFirst() {
                return true;
            }

            @Override
            public boolean isLast() {
                return true;
            }

            @Override
            public boolean hasNext() {
                return false;
            }

            @Override
            public boolean hasPrevious() {
                return false;
            }

            @Override
            public Pageable nextPageable() {
                return null;
            }

            @Override
            public Pageable previousPageable() {
                return null;
            }

            @Override
            public @NotNull Iterator<ResourceRecordView> iterator() {
                return new Iterator<ResourceRecordView>() {
                    @Override
                    public boolean hasNext() {
                        return false;
                    }

                    @Override
                    public ResourceRecordView next() {
                        return null;
                    }
                };
            }
        };
        return page;
    }

    @TestConfiguration
    static class TestConfig {
        @Bean
        public RecordViewService recordViewService() {
            return Mockito.mock(RecordViewService.class);
        }
    }
}