/*
 * This file was last modified at 2026.04.23 20:14 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * JsonRecordServiceImpl.java
 * $Id$
 */

package su.svn.core.services.domain;

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
import su.svn.core.repository.JsonRecordRepository;
import su.svn.core.services.mappers.JsonRecordMapper;

import java.util.Optional;
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

    public static final String ROOT = "root";
    JsonRecordMapper jsonRecordMapper;
    JsonRecordRepository jsonRecordRepository;

    @Override
    public void disable(UUID id) {
        JsonRecord record = null;
        try {
            record = jsonRecordRepository.findByIdAndEnabledTrue(id)
                    .orElseThrow(ChangeSetPersister.NotFoundException::new);
            record.baseRecord().userName(ROOT);
            record.baseRecord().enabled(false);
            record.userName(ROOT);
            record.enabled(false);

            jsonRecordRepository.save(record);
        } catch (ChangeSetPersister.NotFoundException e) {
            System.out.println("Exception: " + e.getMessage());
        }
        // TODO log.info(RecordLogMessages.RECORD_CREATED.getFormatted(savedRecord.getId()));
    }

    @Override
    public ResourceJsonRecord findById(UUID id) throws ChangeSetPersister.NotFoundException {
        JsonRecord record = jsonRecordRepository.findByIdAndEnabledTrue(id)
                .orElseThrow(ChangeSetPersister.NotFoundException::new);
        return jsonRecordMapper.toResource(record);
    }

    @Override
    public ResourceJsonRecord save(NewJsonRecord newRecord) {
        ResourceJsonRecord resourceJsonRecord = jsonRecordMapper.toResource(newRecord);
        JsonRecord record = jsonRecordMapper.toEntity(resourceJsonRecord);
        String username = getUserName();
        record.baseRecord().userName(username);
        record.userName(username);

        JsonRecord savedRecord = jsonRecordRepository.save(record);
        // TODO log.info(RecordLogMessages.RECORD_CREATED.getFormatted(savedRecord.getId()));
        return jsonRecordMapper.toResource(savedRecord);
    }

    @Override
    @Transactional
    public ResourceJsonRecord update(UpdateJsonRecord updateRecord) {
        Optional<JsonRecord> jsonRecord = jsonRecordRepository.findById(updateRecord.id());
        String username = getUserName();
        if (username.equals(jsonRecord.orElseThrow().userName())) {
            ResourceJsonRecord resourceJsonRecord = jsonRecordMapper.toResource(updateRecord);
            JsonRecord record = jsonRecordMapper.toEntity(resourceJsonRecord);
            record.baseRecord().userName(username);
            record.baseRecord().postAt(jsonRecord.orElseThrow().baseRecord().postAt());
            record.userName(username);

            JsonRecord savedRecord = jsonRecordRepository.save(record);
            // TODO log.info(RecordLogMessages.RECORD_CREATED.getFormatted(savedRecord.getId()));
            return jsonRecordMapper.toResource(savedRecord);
        }
        throw new RuntimeException("access decided");
    }

    private static String getUserName() {
        Object principal = SecurityContextHolder.getContext().getAuthentication().getPrincipal();
        String username;
        return switch (principal) {
            case UserName userName -> userName.userName();
            case User user -> user.getUsername();
            default -> "guest";
        };
    }
}
