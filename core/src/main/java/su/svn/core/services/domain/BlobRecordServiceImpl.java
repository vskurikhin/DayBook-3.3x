/*
 * This file was last modified at 2026.05.08 19:33 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * BlobRecordServiceImpl.java
 * $Id$
 */

package su.svn.core.services.domain;

import jakarta.persistence.EntityManager;
import jakarta.transaction.Transactional;
import lombok.RequiredArgsConstructor;
import lombok.experimental.FieldDefaults;
import lombok.extern.slf4j.Slf4j;
import org.springframework.security.core.context.SecurityContextHolder;
import org.springframework.security.core.userdetails.User;
import org.springframework.stereotype.Service;
import su.svn.core.domain.entities.BaseRecord;
import su.svn.core.domain.entities.Tag;
import su.svn.core.domain.entities.UserName;
import su.svn.core.models.dto.NewBlobRecord;
import su.svn.core.models.dto.ResourceBlobRecord;
import su.svn.core.models.dto.UpdateBlobRecord;
import su.svn.core.models.exceptions.CustomNotFoundException;
import su.svn.core.repository.BlobRecordRepository;
import su.svn.core.repository.TagRepository;
import su.svn.core.services.mappers.BlobRecordMapper;

import java.util.ArrayList;
import java.util.List;
import java.util.Set;
import java.util.UUID;
import java.util.stream.Collectors;

import static lombok.AccessLevel.PRIVATE;

@FieldDefaults(level = PRIVATE, makeFinal = true)
@RequiredArgsConstructor
@Service
@Slf4j
public class BlobRecordServiceImpl implements BlobRecordService {

    public static final String GUEST = "guest";

    EntityManager entityManager;

    BlobRecordMapper jsonRecordMapper;
    BlobRecordRepository jsonRecordRepository;

    TagRepository tagRepository;

    @Override
    @Transactional
    public void disable(UUID id) {
        var username = getUserName();
        var record = jsonRecordRepository.findByIdAndEnabledTrue(id)
                .orElseThrow(CustomNotFoundException::new);
        if (username.equals(record.userName())) {
            record.baseRecord().enabled(false);
            record.enabled(false);
            jsonRecordRepository.save(record);
        }
    }

    @Override
    public ResourceBlobRecord findById(UUID id) {
        return jsonRecordMapper.toResource(
                jsonRecordRepository.findByIdAndEnabledTrue(id)
                        .orElseThrow(CustomNotFoundException::new)
        );
    }

    @Override
    @Transactional
    public ResourceBlobRecord save(NewBlobRecord newRecord) {
        var resourceBlobRecord = jsonRecordMapper.toResource(newRecord);
        var jsonRecord = jsonRecordMapper.toEntity(resourceBlobRecord);
        final String username = getUserName();
        jsonRecord.baseRecord().type(su.svn.lib.RecordType.Json);
        jsonRecord.baseRecord().userName(username);
        jsonRecord.userName(username);
        var baseRecord = jsonRecord.baseRecord();
        upTagsInBaseRecordFromDB(baseRecord, newRecord.tags(), username);
        entityManager.persist(baseRecord);
        entityManager.refresh(baseRecord);
        return jsonRecordMapper.toResource(jsonRecordRepository.save(jsonRecord));
    }

    @Override
    @Transactional
    public ResourceBlobRecord update(UpdateBlobRecord updateRecord) {
        var optionalBlobRecord = jsonRecordRepository.findById(updateRecord.id());
        final String username = getUserName();
        if (username.equals(optionalBlobRecord.orElseThrow().userName())) {
            var resourceBlobRecord = jsonRecordMapper.toResource(updateRecord);
            var jsonRecord = jsonRecordMapper.toEntity(resourceBlobRecord);
            jsonRecord.baseRecord()
                    .type(su.svn.lib.RecordType.Json);
            jsonRecord.baseRecord()
                    .postAt(optionalBlobRecord.orElseThrow()
                            .baseRecord()
                            .postAt()
                    );
            jsonRecord.baseRecord()
                    .userName(optionalBlobRecord.orElseThrow()
                            .baseRecord()
                            .userName()
                    );
            jsonRecord.userName(username);
            var baseRecord = jsonRecord.baseRecord();
            upTagsInBaseRecordFromDB(baseRecord, updateRecord.tags(), username);
            return jsonRecordMapper.toResource(jsonRecordRepository.save(jsonRecord));
        }
        throw new RuntimeException("access denied");
    }

    private void upTagsInBaseRecordFromDB(BaseRecord baseRecord, Set<String> tags, final String username) {
        final List<Tag> existingTags = tagRepository.findByTagIn(tags);
        var existingTagNames = existingTags.stream()
                .map(Tag::tag)
                .collect(Collectors.toSet());
        var newTags = tags.stream()
                .filter(tag -> !existingTagNames.contains(tag))
                .map(tag -> Tag.builder()
                        .tag(tag)
                        .userName(username)
                        .build())
                .toList();
        var savedNewTags = tagRepository.saveAll(newTags);
        List<Tag> allTags = new ArrayList<>();
        allTags.addAll(existingTags);
        allTags.addAll(savedNewTags);
        baseRecord.tags(allTags);
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
