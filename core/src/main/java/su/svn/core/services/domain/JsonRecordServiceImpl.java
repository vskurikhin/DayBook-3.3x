/*
 * This file was last modified at 2026.05.03 19:13 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * JsonRecordServiceImpl.java
 * $Id$
 */

package su.svn.core.services.domain;

import jakarta.persistence.EntityManager;
import jakarta.transaction.Transactional;
import lombok.RequiredArgsConstructor;
import lombok.experimental.FieldDefaults;
import lombok.extern.slf4j.Slf4j;
import org.springframework.data.crossstore.ChangeSetPersister;
import org.springframework.security.core.context.SecurityContextHolder;
import org.springframework.security.core.userdetails.User;
import org.springframework.stereotype.Service;
import su.svn.core.domain.entities.JsonRecord;
import su.svn.core.domain.entities.UserName;
import su.svn.core.models.dto.NewJsonRecord;
import su.svn.core.models.dto.ResourceJsonRecord;
import su.svn.core.models.dto.UpdateJsonRecord;
import su.svn.core.models.exceptions.CustomNotFoundException;
import su.svn.core.repository.JsonRecordRepository;
import su.svn.core.services.mappers.JsonRecordMapper;

import java.util.UUID;

import static lombok.AccessLevel.PRIVATE;

/**
 * Implementation of {@link JsonRecordService}.
 *
 * <p>Handles business logic for JSON records, including persistence
 * and mapping between entities and DTOs.</p>
 *
 * @author Victor N. Skurikhin
 */
@FieldDefaults(level = PRIVATE, makeFinal = true)
@RequiredArgsConstructor
@Service
@Slf4j
public class JsonRecordServiceImpl implements JsonRecordService {

    public static final String GUEST = "guest";

    EntityManager entityManager;

    JsonRecordMapper jsonRecordMapper;
    JsonRecordRepository jsonRecordRepository;

    @Override
    @Transactional
    public void disable(UUID id) {
        JsonRecord record = null;
        try {
            var username = getUserName();
            record = jsonRecordRepository.findByIdAndEnabledTrue(id)
                    .orElseThrow(ChangeSetPersister.NotFoundException::new);
            if (username.equals(record.userName())) {
                record.baseRecord().enabled(false);
                record.enabled(false);
                jsonRecordRepository.save(record);
            }
        } catch (ChangeSetPersister.NotFoundException e) {
            System.out.println("Exception: " + e.getMessage());
        }
        // TODO log.info(RecordLogMessages.RECORD_CREATED.getFormatted(savedRecord.getId()));
    }

    @Override
    public ResourceJsonRecord findById(UUID id) {
        return jsonRecordMapper.toResource(jsonRecordRepository.findByIdAndEnabledTrue(id)
                .orElseThrow(CustomNotFoundException::new)
        );
    }

    @Override
    @Transactional
    public ResourceJsonRecord save(NewJsonRecord newRecord) {
        var resourceJsonRecord = jsonRecordMapper.toResource(newRecord);
        var jsonRecord = jsonRecordMapper.toEntity(resourceJsonRecord);
        var username = getUserName();
        jsonRecord.baseRecord().userName(username);
        jsonRecord.userName(username);
        var baseRecord = jsonRecord.baseRecord();
        entityManager.persist(baseRecord);
        entityManager.refresh(baseRecord);
        // TODO log.info(RecordLogMessages.RECORD_CREATED.getFormatted(savedRecord.getId()));
        return jsonRecordMapper.toResource(jsonRecordRepository.save(jsonRecord));
    }

    @Override
    @Transactional
    public ResourceJsonRecord update(UpdateJsonRecord updateRecord) {
        var optionalJsonRecord = jsonRecordRepository.findById(updateRecord.id());
        String username = getUserName();
        if (username.equals(optionalJsonRecord.orElseThrow().userName())) {
            var resourceJsonRecord = jsonRecordMapper.toResource(updateRecord);
            var jsonRecord = jsonRecordMapper.toEntity(resourceJsonRecord);
            jsonRecord.baseRecord()
                    .postAt(optionalJsonRecord.orElseThrow()
                            .baseRecord()
                            .postAt()
                    );
            jsonRecord.baseRecord()
                    .userName(optionalJsonRecord.orElseThrow()
                            .baseRecord()
                            .userName()
                    );
            jsonRecord.userName(username);
            // TODO log.info(RecordLogMessages.RECORD_CREATED.getFormatted(savedRecord.getId()));
            return jsonRecordMapper.toResource(jsonRecordRepository.save(jsonRecord));
        }
        throw new RuntimeException("access denied");
    }

    private static String getUserName() {
        Object principal = SecurityContextHolder.getContext().getAuthentication().getPrincipal();
        return switch (principal) {
            case UserName userName -> userName.userName();
            case User user -> user.getUsername();
            default -> GUEST;
        };
    }
}
