/*
 * This file was last modified at 2026.06.29 16:59 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * RecordViewServiceImpl.java
 * $Id$
 */

package su.svn.core.services.domain;

import lombok.RequiredArgsConstructor;
import lombok.experimental.FieldDefaults;
import lombok.extern.slf4j.Slf4j;
import org.springframework.data.domain.Page;
import org.springframework.data.domain.Pageable;
import org.springframework.data.jpa.domain.Specification;
import org.springframework.stereotype.Service;
import su.svn.core.domain.entities.RecordView;
import su.svn.lib.models.dto.ResourceRecordView;
import su.svn.core.models.dto.ResourceRecordViewFilter;
import su.svn.core.repository.RecordViewRepository;
import su.svn.core.repository.specifications.RecordViewSpecificationBuilder;
import su.svn.core.services.mappers.RecordViewMapper;

import static lombok.AccessLevel.PRIVATE;

/**
 * Implementation of {@link RecordViewService}.
 *
 * <p>Provides filtering and pagination of {@link su.svn.core.domain.entities.RecordView}
 * using specifications.</p>
 *
 * @author Victor N. Skurikhin
 */
@FieldDefaults(level = PRIVATE, makeFinal = true)
@RequiredArgsConstructor
@Service
@Slf4j
public class RecordViewServiceImpl implements RecordViewService {

    RecordViewRepository recordViewRepository;
    RecordViewSpecificationBuilder specificationBuilder;
    RecordViewMapper recordViewMapper;

    @Override
    public Page<ResourceRecordView> getFilteredRecords(ResourceRecordViewFilter filter, Pageable pageable) {
        Specification<RecordView> specification = specificationBuilder.build(filter);
        Page<RecordView> records = recordViewRepository.findAll(specification, pageable);

        return records.map(recordViewMapper::toResource);
    }
}
