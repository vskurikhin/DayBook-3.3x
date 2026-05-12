/*
 * This file was last modified at 2026.05.03 19:13 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * BlobRecordService.java
 * $Id$
 */

package su.svn.core.services.domain;

import su.svn.core.models.dto.NewBlobRecord;
import su.svn.core.models.dto.ResourceBlobRecord;
import su.svn.core.models.dto.UpdateBlobRecord;

import java.util.UUID;

public interface BlobRecordService {

    void disable(UUID id);

    ResourceBlobRecord findById(UUID id);

    ResourceBlobRecord save(NewBlobRecord newRecord);

    ResourceBlobRecord update(UpdateBlobRecord updateRecord);
}
