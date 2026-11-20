import { defineComponent, h, ref, watch } from 'vue';

export const ProductPhoto = defineComponent({
  props: {
    url: {type: String, default: ''},
    name: {type: String, default: ''},
    large: Boolean,
  },
  setup(props) {
    const failed = ref(false);
    watch(() => props.url, () => { failed.value = false; });
    return () => h('div', {class: ['art', {'product-photo-large': props.large}]},
      props.url && !failed.value
        ? h('img', {key: props.url, src: props.url, alt: props.name ? `${props.name}的商品主图` : '商品主图', loading: props.large ? 'eager' : 'lazy', onError: () => { failed.value = true; }})
        : h('svg', {viewBox: '0 0 80 80', 'aria-hidden': 'true'}, [
          h('path', {d: 'M22 25h36l-3 42H25z M29 25v-8q11-10 22 0v8 M32 40h16 M32 47h16 M33 54h14'}),
        ]));
  },
});
