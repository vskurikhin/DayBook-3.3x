/*
 * This file was last modified at 2026.05.22 18:49 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * ValueRecordServiceImpl.java
 * $Id$
 */

package su.svn.core.services.domain;

import jakarta.persistence.EntityManager;
import jakarta.transaction.Transactional;
import lombok.RequiredArgsConstructor;
import lombok.experimental.FieldDefaults;
import lombok.extern.slf4j.Slf4j;
import org.springframework.stereotype.Service;
import su.svn.core.models.dto.NewValueRecord;
import su.svn.core.models.dto.ResourceValueRecord;
import su.svn.core.models.dto.UpdateValueRecord;
import su.svn.core.models.exceptions.CustomNotFoundException;
import su.svn.core.repository.TextRecordRepository;
import su.svn.core.services.mappers.ValueRecordMapper;
import su.svn.lib.RecordType;
import su.svn.lib.TextRecordType;

import java.util.UUID;

import static lombok.AccessLevel.PRIVATE;

@FieldDefaults(level = PRIVATE, makeFinal = true)
@RequiredArgsConstructor
@Service
@Slf4j
public class ValueRecordServiceImpl implements ValueRecordService {

    EntityManager entityManager;

    ValueRecordMapper markdownRecordMapper;
    TextRecordRepository textRecordRepository;

    RecordServiceHelper recordServiceHelper;

    @Override
    @Transactional
    public void disable(UUID id) {
        var username = recordServiceHelper.getUserName();
        var record = textRecordRepository.findByIdAndEnabledTrue(id)
                .orElseThrow(CustomNotFoundException::new);
        if (username.equals(record.userName())) {
            record.baseRecord().enabled(false);
            record.enabled(false);
            textRecordRepository.save(record);
        }
    }

    @Override
    public ResourceValueRecord findById(UUID id) {
        return markdownRecordMapper.toResource(
                textRecordRepository.findByIdAndEnabledTrue(id)
                        .orElseThrow(CustomNotFoundException::new)
        );
    }

    @Override
    @Transactional
    public ResourceValueRecord save(NewValueRecord newRecord) {
        var resourceRecord = markdownRecordMapper.toResource(newRecord);
        var record = markdownRecordMapper.toEntity(resourceRecord);
        final String username = recordServiceHelper.getUserName();
        record.baseRecord().type(RecordType.Text);
        record.baseRecord().userName(username);
        record.userName(username);
        record.type(TextRecordType.Value);
        var baseRecord = record.baseRecord();
        recordServiceHelper.upTagsInBaseRecordFromDB(baseRecord, newRecord.tags(), username);
        entityManager.persist(baseRecord);
        entityManager.refresh(baseRecord);
        return markdownRecordMapper.toResource(textRecordRepository.save(record));
    }

    @Override
    @Transactional
    public ResourceValueRecord update(UpdateValueRecord updateRecord) {
        var optionalRecord = textRecordRepository.findById(updateRecord.id());
        final String username = recordServiceHelper.getUserName();
        if (username.equals(optionalRecord.orElseThrow().userName())) {
            var resourceRecord = markdownRecordMapper.toResource(updateRecord);
            var record = markdownRecordMapper.toEntity(resourceRecord);
            record.baseRecord()
                    .type(RecordType.Text);
            record.baseRecord()
                    .postAt(optionalRecord.orElseThrow()
                            .baseRecord()
                            .postAt()
                    );
            record.baseRecord()
                    .userName(optionalRecord.orElseThrow()
                            .baseRecord()
                            .userName()
                    );
            record.userName(username);
            record.type(TextRecordType.Value);
            var baseRecord = record.baseRecord();
            recordServiceHelper.upTagsInBaseRecordFromDB(baseRecord, updateRecord.tags(), username);
            return markdownRecordMapper.toResource(textRecordRepository.save(record));
        }
        throw new RuntimeException("access denied");
    }
}
