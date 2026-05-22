/*
 * This file was last modified at 2026.05.21 23:42 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * SetRecordServiceImpl.java
 * $Id$
 */

package su.svn.core.services.domain;

import jakarta.persistence.EntityManager;
import jakarta.transaction.Transactional;
import lombok.RequiredArgsConstructor;
import lombok.experimental.FieldDefaults;
import lombok.extern.slf4j.Slf4j;
import org.springframework.stereotype.Service;
import su.svn.core.models.dto.NewSetRecord;
import su.svn.core.models.dto.ResourceSetRecord;
import su.svn.core.models.dto.UpdateSetRecord;
import su.svn.core.models.exceptions.CustomNotFoundException;
import su.svn.core.repository.SetRecordRepository;
import su.svn.core.services.mappers.SetRecordMapper;
import su.svn.lib.RecordType;

import java.util.UUID;

import static lombok.AccessLevel.PRIVATE;

/**
 * Default implementation of {@link SetRecordService}.
 *
 * <p>This service manages persistence and update operations
 * for {@link su.svn.core.domain.entities.SetRecord} entities.</p>
 *
 * <p>The implementation also performs:
 * <ul>
 *     <li>User ownership validation</li>
 *     <li>Tag synchronization</li>
 *     <li>Logical deletion</li>
 * </ul>
 * </p>
 */
@FieldDefaults(level = PRIVATE, makeFinal = true)
@RequiredArgsConstructor
@Service
@Slf4j
public class SetRecordServiceImpl implements SetRecordService {

    EntityManager entityManager;

    SetRecordMapper setRecordMapper;
    SetRecordRepository setRecordRepository;

    RecordServiceHelper recordServiceHelper;

    @Override
    @Transactional
    public void disable(UUID id) {
        var username = recordServiceHelper.getUserName();
        var record = setRecordRepository.findByIdAndEnabledTrue(id)
                .orElseThrow(CustomNotFoundException::new);
        if (username.equals(record.userName())) {
            record.baseRecord().enabled(false);
            record.enabled(false);
            setRecordRepository.save(record);
        }
    }

    @Override
    public ResourceSetRecord findById(UUID id) {
        return setRecordMapper.toResource(
                setRecordRepository.findByIdAndEnabledTrue(id)
                        .orElseThrow(CustomNotFoundException::new)
        );
    }

    @Override
    @Transactional
    public ResourceSetRecord save(NewSetRecord newRecord) {
        var resourceSetRecord = setRecordMapper.toResource(newRecord);
        var jsonRecord = setRecordMapper.toEntity(resourceSetRecord);
        final String username = recordServiceHelper.getUserName();
        jsonRecord.baseRecord().type(RecordType.Set);
        jsonRecord.baseRecord().userName(username);
        jsonRecord.userName(username);
        var baseRecord = jsonRecord.baseRecord();
        recordServiceHelper.upTagsInBaseRecordFromDB(baseRecord, newRecord.tags(), username);
        entityManager.persist(baseRecord);
        entityManager.refresh(baseRecord);
        return setRecordMapper.toResource(setRecordRepository.save(jsonRecord));
    }

    @Override
    @Transactional
    public ResourceSetRecord update(UpdateSetRecord updateRecord) {
        var optionalSetRecord = setRecordRepository.findById(updateRecord.id());
        final String username = recordServiceHelper.getUserName();
        if (username.equals(optionalSetRecord.orElseThrow().userName())) {
            var resourceSetRecord = setRecordMapper.toResource(updateRecord);
            var jsonRecord = setRecordMapper.toEntity(resourceSetRecord);
            jsonRecord.baseRecord()
                    .type(RecordType.Set);
            jsonRecord.baseRecord()
                    .postAt(optionalSetRecord.orElseThrow()
                            .baseRecord()
                            .postAt()
                    );
            jsonRecord.baseRecord()
                    .userName(optionalSetRecord.orElseThrow()
                            .baseRecord()
                            .userName()
                    );
            jsonRecord.userName(username);
            var baseRecord = jsonRecord.baseRecord();
            recordServiceHelper.upTagsInBaseRecordFromDB(baseRecord, updateRecord.tags(), username);
            return setRecordMapper.toResource(setRecordRepository.save(jsonRecord));
        }
        throw new RuntimeException("access denied");
    }
}
