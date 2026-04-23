package su.svn.core.controllers;

import org.jetbrains.annotations.NotNull;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.mockito.Mockito;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.WebMvcTest;
import org.springframework.boot.test.context.TestConfiguration;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Import;
import org.springframework.data.crossstore.ChangeSetPersister;
import org.springframework.data.domain.Page;
import org.springframework.data.domain.Pageable;
import org.springframework.data.domain.Sort;
import org.springframework.security.config.annotation.method.configuration.EnableMethodSecurity;
import org.springframework.security.config.annotation.web.builders.HttpSecurity;
import org.springframework.security.config.annotation.web.configuration.EnableWebSecurity;
import org.springframework.security.config.annotation.web.configurers.AbstractHttpConfigurer;
import org.springframework.security.test.context.support.WithMockUser;
import org.springframework.security.web.SecurityFilterChain;
import org.springframework.test.context.ContextConfiguration;
import org.springframework.test.context.junit.jupiter.SpringExtension;
import org.springframework.test.web.servlet.MockMvc;
import org.springframework.test.web.servlet.MvcResult;
import org.springframework.test.web.servlet.ResultHandler;
import org.springframework.web.cors.CorsConfiguration;
import su.svn.core.domain.entities.UserName;
import su.svn.core.models.dto.ResourceRecordView;
import su.svn.core.services.domain.RecordViewService;
import su.svn.core.services.domain.UserNameService;
import su.svn.core.services.security.JwtService;

import java.time.OffsetDateTime;
import java.util.*;
import java.util.function.Function;

import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.jsonPath;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

@ContextConfiguration
@ExtendWith(SpringExtension.class)
@Import({RecordViewControllerTest.TestConfig.class})
@WebMvcTest(RecordViewController.class)
@WithMockUser(username = RecordViewControllerTest.GUEST, authorities = RecordViewControllerTest.ROLE_GUEST)
class RecordViewControllerTest {

    static final String GUEST = "guest";
    static final String ROLE_GUEST = "GUEST";

    @Autowired
    MockMvc mockMvc;

    @Autowired
    RecordViewService recordViewService;

    @Autowired
    JwtService jwtService;

    @Autowired
    UserNameService userNameService;

    @BeforeEach
    void beforeEach() throws ChangeSetPersister.NotFoundException {
        when(jwtService.extractUserName(any())).thenReturn(GUEST);
        when(jwtService.extractGroups(any())).thenReturn(Set.of(ROLE_GUEST));
        when(jwtService.isTokenValid(any(), any())).thenReturn(true);
        when(userNameService.findByUserName(any())).thenReturn(UserName.builder().userName(GUEST).build());
    }

    // -----------------------------
    // GET
    // -----------------------------
    @Test
    void shouldReturnRecordById() throws Exception {
        UUID id = UUID.randomUUID();

        ResourceRecordView response = ResourceRecordView.builder()
                .id(id)
                .title("test")
                .values(Map.of("key", "value"))
                .postAt(OffsetDateTime.now())
                .visible(true)
                .flags(1)
                .build();
        Page<ResourceRecordView> page = getResourceRecordViews(response);

        when(recordViewService.getFilteredRecords(any(), any())).thenReturn(page);

        mockMvc.perform(get("/core/api/v2/records-view"))
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

        verify(recordViewService).getFilteredRecords(any(), any());
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

        @Bean
        public JwtService jwtService() {
            return Mockito.mock(JwtService.class);
        }

        @Bean
        public UserNameService userNameService() {
            return Mockito.mock(UserNameService.class);
        }
    }


    @EnableWebSecurity
    @EnableMethodSecurity
    @TestConfiguration
    static class TestSecurityConfig {
        @Bean
        public SecurityFilterChain securityFilterChain(HttpSecurity http) throws Exception {
            return http.csrf(AbstractHttpConfigurer::disable)
                    // Своего рода отключение CORS (разрешение запросов со всех доменов)
                    .cors(cors -> cors.configurationSource(request -> {
                        var corsConfiguration = new CorsConfiguration();
                        corsConfiguration.setAllowedOriginPatterns(List.of("*"));
                        corsConfiguration.setAllowedMethods(List.of("GET", "POST", "PUT", "DELETE", "OPTIONS"));
                        corsConfiguration.setAllowedHeaders(List.of("*"));
                        corsConfiguration.setAllowCredentials(true);
                        return corsConfiguration;
                    }))
                    // Настройка доступа к конечным точкам
                    .authorizeHttpRequests(request -> request
                            .requestMatchers("/core/**")
                            .authenticated())
                    .build();
        }
    }
}