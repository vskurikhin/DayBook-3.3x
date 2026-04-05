package su.svn.core;

import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.context.annotation.Import;
import org.springframework.data.crossstore.ChangeSetPersister;
import org.springframework.data.domain.PageRequest;
import org.springframework.transaction.annotation.Transactional;
import su.svn.core.domain.entities.UserName;
import su.svn.core.models.dto.*;
import su.svn.core.repository.JsonRecordRepository;
import su.svn.core.services.domain.JsonRecordServiceImpl;
import su.svn.core.services.domain.RecordViewService;
import su.svn.core.services.domain.UserNameServiceImpl;

import java.time.LocalDateTime;
import java.time.OffsetDateTime;
import java.time.ZoneOffset;
import java.util.UUID;
import java.util.function.Consumer;

import static org.assertj.core.api.Assertions.*;

@SpringBootTest
@Import(TestcontainersConfiguration.class)
@Transactional
class ServicesIT {


    @Autowired
    private JsonRecordServiceImpl jsonRecordService;

    @Autowired
    private JsonRecordRepository jsonRecordRepository;

    @Autowired
    private RecordViewService recordViewService;

    @Autowired
    private UserNameServiceImpl userNameService;

    @Test
    void JsonRecordServiceImpl_shouldSaveAndFindPlusUpdateAndFindUser() throws Exception {
        // given
        String userName = UUID.randomUUID().toString();

        var postAt = OffsetDateTime.of(LocalDateTime.now(), ZoneOffset.UTC);
        NewJsonRecord dto = NewJsonRecord
                .builder()
                .parentId(UUID.fromString("00000000-0000-0000-0000-000000000000"))
                .title("title1")
                .postAt(postAt)
                .build();

        // when
        ResourceJsonRecord saved = jsonRecordService.save(dto);
        jsonRecordRepository.flush();

        // then
        assertThat(saved.id()).isNotNull();

        ResourceJsonRecord found = jsonRecordService.findById(saved.id());
        assertThat(found.title()).isEqualTo("title1");
        assertThat(found.parentId()).isEqualTo(UUID.fromString("00000000-0000-0000-0000-000000000000"));
        assertThat(found.postAt()).isEqualTo(postAt);

        var refreshAt = OffsetDateTime.of(LocalDateTime.now(), ZoneOffset.UTC);

        UpdateJsonRecord upDto = UpdateJsonRecord.builder()
                .id(found.id())
                .parentId(found.parentId())
                .title("title2")
                .values(found.values())
                .refreshAt(refreshAt)
                .build();

        ResourceJsonRecord updated = jsonRecordService.update(upDto);

        ResourceJsonRecord foundUpdated = jsonRecordService.findById(updated.id());

        assertThat(foundUpdated.title()).isEqualTo("title2");
        assertThat(foundUpdated.parentId()).isEqualTo(UUID.fromString("00000000-0000-0000-0000-000000000000"));
        assertThat(foundUpdated.refreshAt()).isEqualTo(refreshAt);

        var result = recordViewService.getFilteredRecords(new ResourceRecordViewFilter(null, null, null, null, false), PageRequest.of(0, 2));
        result.get().forEach(new Consumer<ResourceRecordView>() {
            @Override
            public void accept(ResourceRecordView resourceRecordView) {
                System.out.println("resourceRecordView: " + resourceRecordView);
            }
        });
        System.out.println("result: " + result);
        jsonRecordService.disable(updated.id());

        assertThatExceptionOfType(ChangeSetPersister.NotFoundException.class)
                .isThrownBy(() -> jsonRecordService.findById(updated.id()));
    }

    @Test
    void UserNameServiceImpl_shouldSaveAndFindUser() throws Exception {
        // given
        String userName = UUID.randomUUID().toString();
        NewUserName dto = NewUserName
                .builder()
                .userName(userName)
                .id(UUID.randomUUID())
                .build();

        // when
        UserName saved = userNameService.save(dto);

        // then
        assertThat(saved.id()).isNotNull();

        UserName found = userNameService.findByUserName(userName);
        assertThat(found.userName()).isEqualTo(userName);
    }

    @Test
    void UserNameServiceImpl_shouldThrowWhenUserNotFound() {
        assertThatThrownBy(() -> userNameService.findByUserName("unknown"))
                .isInstanceOf(Exception.class);
    }
}