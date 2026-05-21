package su.svn.api.domain.entities;

import org.junit.jupiter.api.Test;

import java.time.Duration;
import java.time.LocalDateTime;
import java.time.OffsetDateTime;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.UUID;

import static org.assertj.core.api.Assertions.assertThat;

class PostRecordTest {

    @Test
    void shouldBuildEntity() {
        var id = UUID.randomUUID();
        var parentId = UUID.randomUUID();

        var json = new LinkedHashMap<String, String>();
        json.put("k", "v");

        var now = OffsetDateTime.now();

        var entity = PostRecord.builder()
                .id(id)
                .parentId(parentId)
                .userName("admin")
                .postAt(now)
                .refreshAt(now)
                .visible(true)
                .flags(100)
                .title("title")
                .blob(new byte[]{1, 2})
                .json(json)
                .build();

        assertThat(entity.id()).isEqualTo(id);
        assertThat(entity.parentId()).isEqualTo(parentId);
        assertThat(entity.userName()).isEqualTo("admin");
        assertThat(entity.postAt()).isEqualTo(now);
        assertThat(entity.refreshAt()).isEqualTo(now);
        assertThat(entity.visible()).isTrue();
        assertThat(entity.flags()).isEqualTo(100);
        assertThat(entity.title()).isEqualTo("title");
        assertThat(entity.blob()).containsExactly(1, 2);
        assertThat(entity.json()).containsEntry("k", "v");
    }

    @Test
    void shouldUseDefaultValues() {
        var entity = PostRecord.builder().build();

        assertThat(entity.type())
                .isEqualTo(su.svn.lib.RecordType.Base);

        assertThat(entity.enabled()).isTrue();

        assertThat(entity.localChange()).isTrue();
    }

    @Test
    void shouldSetFieldsUsingAccessors() {
        var entity = new PostRecord();

        var id = UUID.randomUUID();
        var now = LocalDateTime.now();

        entity.id(id);
        entity.flags(10);
        entity.title("updated");
        entity.lastChangedTime(now);

        assertThat(entity.id()).isEqualTo(id);
        assertThat(entity.flags()).isEqualTo(10);
        assertThat(entity.title()).isEqualTo("updated");
        assertThat(entity.lastChangedTime()).isEqualTo(now);
    }

    @Test
    void shouldContainConstants() {
        assertThat(PostRecord.TIMEOUT_DURATION)
                .isEqualTo(Duration.ofMillis(2000));

        assertThat(PostRecord.FIND_FIND_BY_UUID)
                .isEqualTo("PostRecord.findByUUID");

        assertThat(PostRecord.FIND_LAST_CHANGED_TIME_POST_RECORD)
                .isEqualTo("PostRecord.findLastChangedTimePostRecord");

        assertThat(PostRecord.READ_ENABLED_AND_ID_IN)
                .isEqualTo("PostRecord.readEnabledAndIdIn");

        assertThat(PostRecord.READ_ENABLED_ORDER_POST_REFRESH_DESC)
                .isEqualTo("PostRecord.readEnabledOrderByPostAtAndRefreshAtDesc");

        assertThat(PostRecord.ENABLED)
                .isEqualTo(Map.of("enabled", Boolean.TRUE));
    }

    @Test
    void shouldSupportEqualsHashCodeToString() {
        var id = UUID.randomUUID();

        var left = PostRecord.builder()
                .id(id)
                .title("same")
                .build();

        var right = PostRecord.builder()
                .id(id)
                .title("same")
                .build();

        assertThat(left)
                .isEqualTo(right);

        assertThat(left.hashCode())
                .isEqualTo(right.hashCode());

        assertThat(left.toString())
                .contains("same");
    }
}