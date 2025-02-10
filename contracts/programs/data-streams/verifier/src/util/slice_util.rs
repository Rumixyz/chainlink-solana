pub struct SliceUtil {}

impl SliceUtil {
    pub fn has_duplicates(v: &[[u8; 20]]) -> bool {
        for i in 0..v.len() {
            for j in (i + 1)..v.len() {
                if v[i] == v[j] {
                    return true;
                }
            }
        }
        false
    }

    pub fn has_duplicates_sorted(v: &[[u8; 20]]) -> bool {
        v.windows(2).any(|w| w[0] == w[1])
    }
}
