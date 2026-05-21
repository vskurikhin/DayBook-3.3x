/*
 * This file was last modified at 2026.05.21 16:49 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * PagedModelEntityModelResourceRecordViewEmbedded.java
 * $Id$
 */

package su.svn.api.models.dto;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.annotation.JsonProperty;
import lombok.Builder;

import java.io.Serializable;
import java.util.ArrayList;
import java.util.List;

/**
 * PagedModelEntityModelResourceRecordViewEmbedded
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public record PagedModelEntityModelResourceRecordViewEmbedded(
        @JsonProperty List<EntityModelResourceRecordView> resourceRecordViewList
) implements Serializable {

    @SuppressWarnings("ReassignedVariable")
    @Builder
    public PagedModelEntityModelResourceRecordViewEmbedded {
        if (resourceRecordViewList == null) resourceRecordViewList = new ArrayList<>();
    }

    public PagedModelEntityModelResourceRecordViewEmbedded() {
        this(new ArrayList<>());
    }
}
