/*
 * This file was last modified at 2026.03.27 14:01 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * RecordViewService.java
 * $Id$
 */

package su.svn.core.services.domain;

import org.springframework.data.domain.Page;
import org.springframework.data.domain.Pageable;
import su.svn.core.models.dto.ResourceRecordView;
import su.svn.core.models.dto.ResourceRecordViewFilter;

/**
 * Service interface for retrieving filtered record views.
 *
 * <p>Supports pagination and dynamic filtering.</p>
 */
public interface RecordViewService {
    Page<ResourceRecordView> getFilteredRecords(ResourceRecordViewFilter filter, Pageable pageable);
}
